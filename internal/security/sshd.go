package security

import (
	"fmt"
	"regexp"
	"strings"
)

// SSHSettings are the few sshd options Islet manages.
type SSHSettings struct {
	Port           int  `json:"port"`
	PermitRoot     bool `json:"permitRootLogin"`
	PasswordAuth   bool `json:"passwordAuth"`
	PubkeyAuth     bool `json:"pubkeyAuth"`
	MaxAuthTries   int  `json:"maxAuthTries"`
	AllowAgentFwd  bool `json:"allowAgentForwarding"`
	X11Forwarding  bool `json:"x11Forwarding"`
	ClientAliveMax int  `json:"clientAliveCountMax"`
}

var managedKeys = map[string]bool{"Port": true, "PermitRootLogin": true, "PasswordAuthentication": true, "PubkeyAuthentication": true, "MaxAuthTries": true, "AllowAgentForwarding": true, "X11Forwarding": true, "ClientAliveCountMax": true, "ChallengeResponseAuthentication": true, "KbdInteractiveAuthentication": true}

var kvRe = regexp.MustCompile(`^\s*([A-Za-z]+)\s+(.+?)\s*$`)

// ParseSSHD reads the effective values of the managed keys from an sshd_config
// (first occurrence wins, matching sshd; Match blocks are skipped).
func ParseSSHD(text string) SSHSettings {
	s := SSHSettings{Port: 22, PermitRoot: true, PasswordAuth: true, PubkeyAuth: true, MaxAuthTries: 6, AllowAgentFwd: true, X11Forwarding: false, ClientAliveMax: 3}
	seen := map[string]bool{}
	inMatch := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		m := kvRe.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		key := m[1]
		if strings.EqualFold(key, "Match") {
			inMatch = true
			continue
		}
		if inMatch {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		v := strings.ToLower(m[2])
		yes := v == "yes"
		switch key {
		case "Port":
			fmt.Sscanf(v, "%d", &s.Port)
		case "PermitRootLogin":
			s.PermitRoot = yes || v == "without-password" || v == "prohibit-password"
			if v == "prohibit-password" || v == "without-password" {
				s.PermitRoot = false // key-only root is treated as hardened
			}
		case "PasswordAuthentication":
			s.PasswordAuth = yes
		case "PubkeyAuthentication":
			s.PubkeyAuth = yes
		case "MaxAuthTries":
			fmt.Sscanf(v, "%d", &s.MaxAuthTries)
		case "AllowAgentForwarding":
			s.AllowAgentFwd = yes
		case "X11Forwarding":
			s.X11Forwarding = yes
		case "ClientAliveCountMax":
			fmt.Sscanf(v, "%d", &s.ClientAliveMax)
		}
	}
	return s
}

// RenderSSHD writes Islet's managed block. It goes into
// /etc/ssh/sshd_config.d/00-islet.conf, which sshd reads before the main
// file, so these values win without editing the distribution's config.
func RenderSSHD(s SSHSettings) string {
	yn := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	root := "no"
	if s.PermitRoot {
		root = "yes"
	}
	return fmt.Sprintf(`# Managed by Islet. Edit in the panel (Security → SSH); manual changes are overwritten.
Port %d
PermitRootLogin %s
PasswordAuthentication %s
KbdInteractiveAuthentication %s
ChallengeResponseAuthentication %s
PubkeyAuthentication %s
MaxAuthTries %d
AllowAgentForwarding %s
X11Forwarding %s
ClientAliveInterval 300
ClientAliveCountMax %d
LoginGraceTime 30
`, s.Port, root, yn(s.PasswordAuth), yn(s.PasswordAuth), yn(s.PasswordAuth), yn(s.PubkeyAuth), s.MaxAuthTries, yn(s.AllowAgentFwd), yn(s.X11Forwarding), s.ClientAliveMax)
}

// Validate refuses settings that would lock everyone out.
func (s SSHSettings) Validate(hasAuthorizedKeys bool) error {
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if !s.PasswordAuth && !s.PubkeyAuth {
		return fmt.Errorf("at least one of password or public key authentication must stay on")
	}
	if !s.PasswordAuth && !hasAuthorizedKeys {
		return fmt.Errorf("no authorized_keys found for any login user; add your SSH key before turning off passwords")
	}
	if s.MaxAuthTries < 1 || s.MaxAuthTries > 20 {
		return fmt.Errorf("MaxAuthTries must be between 1 and 20")
	}
	if s.ClientAliveMax < 0 || s.ClientAliveMax > 100 {
		return fmt.Errorf("ClientAliveCountMax must be between 0 and 100")
	}
	return nil
}
