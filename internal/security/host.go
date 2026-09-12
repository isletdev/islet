package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

var (
	userRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	keyRe  = regexp.MustCompile(`^(ssh-(ed25519|rsa)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256))@?\S* [A-Za-z0-9+/=]+( .*)?$`)
	tzRe   = regexp.MustCompile(`^[A-Za-z_]+(/[A-Za-z0-9_+-]+){0,2}$`)
)

// CreateSudoUser adds a user with passwordless sudo and, when given, an
// SSH public key. Day-to-day logins then need not be root.
func (s *Service) CreateSudoUser(ctx context.Context, actor, name, pubkey string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("host changes run on Linux servers only")
	}
	if !userRe.MatchString(name) || name == "root" {
		return "", errors.New("user names are lowercase letters, digits, dashes and underscores")
	}
	if _, err := s.sh(ctx, actor, "id", name); err == nil {
		return "", errors.New("user " + name + " already exists")
	}
	out, err := s.sh(ctx, actor, "sh", "-c", "useradd -m -s /bin/bash "+name+" && usermod -aG sudo "+name+" 2>/dev/null || usermod -aG wheel "+name)
	if err != nil {
		return out, err
	}
	sudoers := "/etc/sudoers.d/90-islet-" + name
	if err := os.WriteFile(sudoers, []byte(name+" ALL=(ALL) NOPASSWD:ALL\n"), 0o440); err != nil {
		return out, err
	}
	if strings.TrimSpace(pubkey) != "" {
		if o, err := s.AddSSHKey(ctx, actor, name, pubkey); err != nil {
			return out + o, err
		}
	}
	_ = s.st.Audit(ctx, actor, "host.user", name, "sudo user created")
	return out + "user " + name + " created with passwordless sudo", nil
}

// AddSSHKey appends a public key to a user's authorized_keys.
func (s *Service) AddSSHKey(ctx context.Context, actor, user, pubkey string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("host changes run on Linux servers only")
	}
	pubkey = strings.TrimSpace(pubkey)
	if !userRe.MatchString(user) && user != "root" {
		return "", errors.New("invalid user name")
	}
	if !keyRe.MatchString(pubkey) || strings.Contains(pubkey, "\n") {
		return "", errors.New("paste one public key line (ssh-ed25519 AAAA… comment)")
	}
	home := "/root"
	if user != "root" {
		home = "/home/" + user
		if _, err := os.Stat(home); err != nil {
			return "", errors.New("user " + user + " has no home directory")
		}
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "authorized_keys")
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), strings.Fields(pubkey)[1]) {
		return "key already present", nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(pubkey + "\n")
	f.Close()
	if err != nil {
		return "", err
	}
	_, _ = s.sh(ctx, actor, "chown", "-R", user+":"+user, dir)
	_ = s.st.Audit(ctx, actor, "host.sshkey", user, strings.Fields(pubkey)[0])
	return "key added to " + path, nil
}

// SetTimezone changes the system timezone through timedatectl.
func (s *Service) SetTimezone(ctx context.Context, actor, tz string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("host changes run on Linux servers only")
	}
	if !tzRe.MatchString(tz) {
		return "", errors.New("timezone must be an IANA name such as Europe/Skopje")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", errors.New("unknown timezone " + tz)
	}
	out, err := s.sh(ctx, actor, "timedatectl", "set-timezone", tz)
	if err == nil {
		_ = s.st.Audit(ctx, actor, "host.timezone", tz, "")
	}
	return out, err
}

// Timezone reports the current system timezone.
func (s *Service) Timezone(ctx context.Context) string {
	if runtime.GOOS != "linux" {
		return time.Local.String()
	}
	out, err := s.sh(ctx, "system", "timedatectl", "show", "-p", "Timezone", "--value")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HostAudit is the result of the filesystem checks.
type HostAudit struct {
	At            string   `json:"at"`
	SUID          []string `json:"suid"`          // setuid/setgid binaries outside the known set
	WorldWritable []string `json:"worldWritable"` // world-writable files outside /tmp and friends
	EtcChanged    []string `json:"etcChanged"`
	EtcAdded      []string `json:"etcAdded"`
	EtcRemoved    []string `json:"etcRemoved"`
	BaselineAt    string   `json:"baselineAt"`
	Rkhunter      string   `json:"rkhunter"` // warnings from rkhunter, empty when clean or not run
	RkhunterRan   bool     `json:"rkhunterRan"`
	Notes         []string `json:"notes"`
}

// knownSUID are setuid binaries every Debian/Ubuntu box has.
var knownSUID = map[string]bool{
	"/usr/bin/sudo": true, "/usr/bin/su": true, "/usr/bin/passwd": true, "/usr/bin/chsh": true, "/usr/bin/chfn": true, "/usr/bin/gpasswd": true,
	"/usr/bin/newgrp": true, "/usr/bin/mount": true, "/usr/bin/umount": true, "/usr/bin/fusermount": true, "/usr/bin/fusermount3": true, "/usr/bin/pkexec": true,
	"/usr/lib/openssh/ssh-keysign": true, "/usr/lib/dbus-1.0/dbus-daemon-launch-helper": true, "/usr/bin/ssh-agent": true, "/usr/bin/wall": true,
	"/usr/bin/expiry": true, "/usr/bin/chage": true, "/usr/bin/crontab": true, "/usr/bin/at": true, "/usr/sbin/unix_chkpwd": true, "/usr/sbin/pam_extrausers_chkpwd": true,
	"/usr/lib/polkit-1/polkit-agent-helper-1": true, "/usr/libexec/polkit-agent-helper-1": true, "/usr/bin/bsd-write": true, "/usr/lib/snapd/snap-confine": true,
	"/usr/bin/ping": true, "/usr/bin/dotlockfile": true, "/usr/sbin/pppd": true, "/usr/lib/x86_64-linux-gnu/utempter/utempter": true, "/usr/bin/ntfs-3g": true,
}

func (s *Service) auditPath() string { return filepath.Join(s.dataDir, "security", "host-audit.json") }
func (s *Service) baselinePath() string {
	return filepath.Join(s.dataDir, "security", "etc-baseline.json")
}

// etcHashes hashes every regular file under /etc (small files only; keys
// and databases are skipped by size).
func etcHashes() (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir("/etc", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 4<<20 {
			return nil
		}
		if strings.HasPrefix(path, "/etc/ld.so.cache") || strings.HasPrefix(path, "/etc/mtab") || strings.Contains(path, "/etc/islet/") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		h := sha256.New()
		_, _ = io.Copy(h, f)
		f.Close()
		out[path] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return out, err
}

// SetBaseline records the current /etc hashes as the reference.
func (s *Service) SetBaseline(ctx context.Context, actor string) (int, error) {
	if runtime.GOOS != "linux" {
		return 0, errors.New("host audits run on Linux servers only")
	}
	h, err := etcHashes()
	if err != nil {
		return 0, err
	}
	b, _ := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "files": h})
	if err := os.MkdirAll(filepath.Dir(s.baselinePath()), 0o750); err != nil {
		return 0, err
	}
	if err := os.WriteFile(s.baselinePath(), b, 0o600); err != nil {
		return 0, err
	}
	_ = s.st.Audit(ctx, actor, "security.baseline", "/etc", fmt.Sprintf("%d files", len(h)))
	return len(h), nil
}

// RunHostAudit performs the SUID, world-writable and /etc integrity checks
// and, when withRkhunter is set, installs and runs rkhunter.
func (s *Service) RunHostAudit(ctx context.Context, actor string, withRkhunter bool) (*HostAudit, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("host audits run on Linux servers only")
	}
	a := &HostAudit{At: time.Now().UTC().Format(time.RFC3339), SUID: []string{}, WorldWritable: []string{}, EtcChanged: []string{}, EtcAdded: []string{}, EtcRemoved: []string{}, Notes: []string{}}
	if out, err := s.sh(ctx, actor, "sh", "-c", "find / -xdev \\( -perm -4000 -o -perm -2000 \\) -type f 2>/dev/null"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" && !knownSUID[l] {
				a.SUID = append(a.SUID, l)
			}
		}
	}
	if out, err := s.sh(ctx, actor, "sh", "-c", "find / -xdev -type f -perm -0002 -not -path '/proc/*' -not -path '/tmp/*' -not -path '/var/tmp/*' -not -path '/dev/*' -not -path '/var/lib/docker/*' 2>/dev/null | head -n 200"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" {
				a.WorldWritable = append(a.WorldWritable, l)
			}
		}
	}
	if b, err := os.ReadFile(s.baselinePath()); err == nil {
		var base struct {
			At    string            `json:"at"`
			Files map[string]string `json:"files"`
		}
		if json.Unmarshal(b, &base) == nil {
			a.BaselineAt = base.At
			if now, err := etcHashes(); err == nil {
				for p, h := range now {
					if old, ok := base.Files[p]; !ok {
						a.EtcAdded = append(a.EtcAdded, p)
					} else if old != h {
						a.EtcChanged = append(a.EtcChanged, p)
					}
				}
				for p := range base.Files {
					if _, ok := now[p]; !ok {
						a.EtcRemoved = append(a.EtcRemoved, p)
					}
				}
				sort.Strings(a.EtcAdded)
				sort.Strings(a.EtcChanged)
				sort.Strings(a.EtcRemoved)
			}
		}
	} else {
		a.Notes = append(a.Notes, "No /etc baseline yet. Set one now while the server is in a known-good state; later audits list every change.")
	}
	if withRkhunter {
		if !has("rkhunter") {
			if _, err := s.aptInstall(ctx, actor, "rkhunter"); err != nil {
				a.Notes = append(a.Notes, "rkhunter could not be installed: "+err.Error())
			}
		}
		if has("rkhunter") {
			_, _ = s.sh(ctx, actor, "rkhunter", "--propupd", "--quiet")
			out, _ := s.sh(ctx, actor, "sh", "-c", "rkhunter --check --sk --rwo --nocolors 2>&1 | grep -v '^$' | head -n 60")
			a.Rkhunter = strings.TrimSpace(out)
			a.RkhunterRan = true
		}
	}
	sort.Strings(a.SUID)
	b, _ := json.Marshal(a)
	_ = os.MkdirAll(filepath.Dir(s.auditPath()), 0o750)
	_ = os.WriteFile(s.auditPath(), b, 0o600)
	_ = s.st.Audit(ctx, actor, "security.audit", "host", fmt.Sprintf("%d suid, %d world-writable, %d etc changes", len(a.SUID), len(a.WorldWritable), len(a.EtcChanged)+len(a.EtcAdded)+len(a.EtcRemoved)))
	return a, nil
}

// LastHostAudit returns the stored result, if any.
func (s *Service) LastHostAudit() *HostAudit {
	b, err := os.ReadFile(s.auditPath())
	if err != nil {
		return nil
	}
	var a HostAudit
	if json.Unmarshal(b, &a) != nil {
		return nil
	}
	return &a
}
