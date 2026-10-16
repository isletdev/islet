package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Lockdown is "the panel and SSH only over the VPN".
//
// It is two layers, because either alone has a hole. The firewall closes the
// panel port and the SSH port to everything but the tunnel's range. That does
// nothing for a panel published on a domain, which arrives through the proxy
// on 443, so the daemon also refuses to sign anybody in, or serve a signed-in
// request, unless it comes from the tunnel, from this machine, or from the
// one fallback address the operator chose to keep.
//
// It undoes itself. Turning it on starts a two-minute clock, and only a
// confirmation sent from inside the tunnel stops it — which proves, rather
// than assumes, that the person who turned it on can still get in. A daemon
// that restarts while the clock runs undoes it on the way up.
type Lockdown struct {
	Panel     bool   `json:"panel"`
	SSH       bool   `json:"ssh"`
	PanelPort int    `json:"panelPort,omitempty"`
	SSHPort   int    `json:"sshPort,omitempty"`
	KeepIP    string `json:"keepIp,omitempty"`
	Subnet    string `json:"subnet,omitempty"`
	Pending   bool   `json:"pending"`
	Deadline  string `json:"deadline,omitempty"`
	// Removed are the ufw rules this took away, exactly as `ufw show added`
	// printed them, so undoing puts back what was there rather than what
	// somebody guessed was there.
	Removed []string `json:"removed,omitempty"`
}

// Active reports whether any part of it is on.
func (l Lockdown) Active() bool { return l.Panel || l.SSH }

// LockdownInput is what the panel asks for.
type LockdownInput struct {
	Panel     bool   `json:"panel"`
	SSH       bool   `json:"ssh"`
	KeepIP    string `json:"keepIp"`
	PanelPort int    `json:"-"`
	SSHPort   int    `json:"-"`
}

const (
	keyLockdown = "vpn.lockdown"
	// ConfirmWindow is how long a new lockdown waits to be confirmed from
	// inside the tunnel before it undoes itself.
	ConfirmWindow = 2 * time.Minute
	lockComment   = "Islet VPN only"
	keepComment   = "Islet VPN fallback"
)

// Lockdown returns the current state, from memory: the gate asks on every
// request.
func (s *Service) Lockdown() Lockdown {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	return s.lock
}

func (s *Service) loadLockdown(ctx context.Context) Lockdown {
	var l Lockdown
	if v, _, _ := s.st.Setting(ctx, keyLockdown); v != "" {
		_ = json.Unmarshal([]byte(v), &l)
	}
	return l
}

func (s *Service) storeLockdown(ctx context.Context, l Lockdown) error {
	b, _ := json.Marshal(l)
	if err := s.st.SetSetting(ctx, keyLockdown, string(b)); err != nil {
		return err
	}
	s.lockMu.Lock()
	s.lock = l
	s.lockMu.Unlock()
	return nil
}

// Allowed is the daemon's half of the lockdown: may a request from this
// address sign in, or act signed in?
func (s *Service) Allowed(ip string) bool {
	l := s.Lockdown()
	if !l.Panel {
		return true
	}
	return allowedFrom(l, ip)
}

func allowedFrom(l Lockdown, ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	if a.IsLoopback() {
		return true // this machine: fleet tunnels, workspaces, the CLI
	}
	if p, err := netip.ParsePrefix(l.Subnet); err == nil && p.Contains(a) {
		return true
	}
	if l.KeepIP != "" {
		if k, err := netip.ParseAddr(l.KeepIP); err == nil && k.Unmap() == a {
			return true
		}
	}
	return false
}

// InTunnel reports whether an address is inside the VPN's range.
func (s *Service) InTunnel(ctx context.Context, ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	p, err := netip.ParsePrefix(s.Config(ctx).Subnet)
	return err == nil && p.Contains(a.Unmap())
}

// SetLockdown turns parts of it on or off. clientIP is who asked, for the
// audit and for the refusal below.
func (s *Service) SetLockdown(ctx context.Context, actor string, in LockdownInput, clientIP string) error {
	ctx = context.WithoutCancel(ctx)
	cur := s.loadLockdown(ctx)
	// Off first, always: whatever is on is undone completely before anything
	// new is applied, so turning SSH off while keeping the panel is "undo
	// both, apply the panel" — one code path instead of four.
	if cur.Active() {
		if err := s.undo(ctx, actor, cur); err != nil {
			return err
		}
	}
	if !in.Panel && !in.SSH {
		_ = s.st.Audit(ctx, actor, "vpn.lockdown.off", "", clientIP)
		return nil
	}
	c := s.Config(ctx)
	if !c.Enabled {
		return errors.New("turn the VPN on first")
	}
	if s.adminPeers(ctx, "") == 0 {
		return errors.New("add an admin device and connect it first; the lockdown would otherwise lock everybody out")
	}
	if in.KeepIP != "" {
		a, err := netip.ParseAddr(strings.TrimSpace(in.KeepIP))
		if err != nil {
			return errors.New("the fallback address must be an IP address")
		}
		in.KeepIP = a.Unmap().String()
	}
	l := Lockdown{Panel: in.Panel, SSH: in.SSH, PanelPort: in.PanelPort, SSHPort: in.SSHPort, KeepIP: in.KeepIP, Subnet: c.Subnet,
		Pending: true, Deadline: s.now().Add(ConfirmWindow).UTC().Format(time.RFC3339)}
	if l.PanelPort == 0 && s.hooks.PanelPort != nil {
		l.PanelPort, _ = strconv.Atoi(s.hooks.PanelPort())
	}
	if l.PanelPort == 0 {
		l.PanelPort = 9443
	}
	if l.SSHPort == 0 {
		l.SSHPort = 22
	}
	// Recorded before the firewall changes, so a crash half way through still
	// leaves enough on disk for the next start to undo it.
	if err := s.storeLockdown(ctx, l); err != nil {
		return err
	}
	if s.ufwActive(ctx) {
		var ports []int
		if l.Panel {
			ports = append(ports, l.PanelPort)
		}
		if l.SSH {
			ports = append(ports, l.SSHPort)
		}
		for _, p := range ports {
			ps := strconv.Itoa(p)
			if _, err := s.sh(ctx, actor, nil, "ufw", "allow", "from", l.Subnet, "to", "any", "port", ps, "proto", "tcp", "comment", lockComment); err != nil {
				_ = s.undo(ctx, actor, l)
				return err
			}
			if l.KeepIP != "" {
				if _, err := s.sh(ctx, actor, nil, "ufw", "allow", "from", l.KeepIP, "to", "any", "port", ps, "proto", "tcp", "comment", keepComment); err != nil {
					_ = s.undo(ctx, actor, l)
					return err
				}
			}
		}
		added, err := s.sh(ctx, actor, nil, "ufw", "show", "added")
		if err != nil {
			_ = s.undo(ctx, actor, l)
			return err
		}
		for _, rule := range PublicRules(added, ports) {
			if _, err := s.sh(ctx, actor, nil, "ufw", append([]string{"delete"}, stripComment(rule)...)...); err != nil {
				_ = s.undo(ctx, actor, l)
				return err
			}
			l.Removed = append(l.Removed, strings.Join(rule, "\x1f"))
			if err := s.storeLockdown(ctx, l); err != nil {
				return err
			}
		}
	} else if l.SSH {
		_ = s.undo(ctx, actor, l)
		return errors.New("SSH can only be limited to the VPN with ufw active; turn the firewall on from the Security page")
	}
	_ = s.st.Audit(ctx, actor, "vpn.lockdown.on", describe(l), clientIP)
	s.arm(l)
	return nil
}

// Confirm keeps a pending lockdown. It has to come from inside the tunnel:
// that is the whole proof that the tunnel works for the person who will need
// it.
func (s *Service) Confirm(ctx context.Context, actor, clientIP string) error {
	l := s.loadLockdown(ctx)
	if !l.Active() || !l.Pending {
		return errors.New("nothing is waiting to be confirmed")
	}
	if !s.InTunnel(ctx, clientIP) {
		return fmt.Errorf("confirm from a device connected to the VPN, at https://%s:%d — this request came from %s", s.Config(ctx).Address(), l.PanelPort, clientIP)
	}
	s.lockMu.Lock()
	if s.lockTime != nil {
		s.lockTime.Stop()
		s.lockTime = nil
	}
	s.lockMu.Unlock()
	l.Pending, l.Deadline = false, ""
	if err := s.storeLockdown(ctx, l); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "vpn.lockdown.confirm", describe(l), clientIP)
	return nil
}

func (s *Service) arm(l Lockdown) {
	d := ConfirmWindow
	if t, err := time.Parse(time.RFC3339, l.Deadline); err == nil {
		d = time.Until(t)
	}
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lockTime != nil {
		s.lockTime.Stop()
	}
	s.lockTime = time.AfterFunc(d, func() {
		ctx := context.Background()
		cur := s.loadLockdown(ctx)
		if !cur.Pending {
			return
		}
		_ = s.undo(ctx, "system", cur)
		_ = s.st.Audit(ctx, "system", "vpn.lockdown.revert", describe(cur), "not confirmed from inside the VPN in time")
		s.emit(ctx, "warning", "VPN lockdown undone", "The panel and SSH were limited to the VPN, and nobody confirmed from inside it within two minutes, so they are open again as before.", "")
	})
}

// undo puts the firewall back and clears the state. Each step is tolerant of
// its work already being done.
func (s *Service) undo(ctx context.Context, actor string, l Lockdown) error {
	s.lockMu.Lock()
	if s.lockTime != nil {
		s.lockTime.Stop()
		s.lockTime = nil
	}
	s.lockMu.Unlock()
	if s.ufwActive(ctx) {
		// What was removed comes back first, so there is never a moment with
		// neither the old rule nor ours.
		for _, r := range l.Removed {
			_, _ = s.sh(ctx, actor, nil, "ufw", strings.Split(r, "\x1f")...)
		}
		for _, p := range []struct {
			on   bool
			port int
		}{{l.Panel, l.PanelPort}, {l.SSH, l.SSHPort}} {
			if !p.on || p.port == 0 {
				continue
			}
			ps := strconv.Itoa(p.port)
			_, _ = s.sh(ctx, actor, nil, "ufw", "delete", "allow", "from", l.Subnet, "to", "any", "port", ps, "proto", "tcp")
			if l.KeepIP != "" {
				_, _ = s.sh(ctx, actor, nil, "ufw", "delete", "allow", "from", l.KeepIP, "to", "any", "port", ps, "proto", "tcp")
			}
		}
	}
	return s.storeLockdown(ctx, Lockdown{})
}

func describe(l Lockdown) string {
	var p []string
	if l.Panel {
		p = append(p, "panel")
	}
	if l.SSH {
		p = append(p, "ssh")
	}
	s := strings.Join(p, "+")
	if l.KeepIP != "" {
		s += ", fallback " + l.KeepIP
	}
	return s
}

// PublicRules picks, out of `ufw show added`, the rules that open one of
// ports to everybody: no "from", a matching port or the OpenSSH profile.
// Each comes back as its words after "ufw", ready to be deleted and later
// put back.
func PublicRules(added string, ports []int) [][]string {
	want := map[string]bool{}
	for _, p := range ports {
		ps := strconv.Itoa(p)
		want[ps], want[ps+"/tcp"] = true, true
		if p == 22 {
			want["OpenSSH"] = true
		}
	}
	var out [][]string
	for _, line := range strings.Split(added, "\n") {
		w := shellWords(strings.TrimSpace(line))
		if len(w) < 3 || w[0] != "ufw" {
			continue
		}
		w = w[1:]
		i := 0
		if w[i] != "allow" && w[i] != "limit" {
			continue
		}
		i++
		if i < len(w) && w[i] == "in" {
			i++
		}
		if i >= len(w) || !want[w[i]] {
			continue
		}
		rest := w[i+1:]
		if len(rest) > 0 && rest[0] != "comment" {
			continue // a from, an interface, anything that narrows it
		}
		out = append(out, w)
	}
	return out
}

// stripComment drops a trailing comment, which `ufw delete` does not need.
func stripComment(w []string) []string {
	for i, x := range w {
		if x == "comment" {
			return w[:i]
		}
	}
	return w
}

// shellWords splits the way `ufw show added` quotes: single quotes around a
// comment, nothing fancier.
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	in, any := false, false
	for _, r := range s {
		switch {
		case r == '\'':
			in, any = !in, true
		case r == ' ' && !in:
			if any {
				out = append(out, cur.String())
				cur.Reset()
				any = false
			}
		default:
			cur.WriteRune(r)
			any = true
		}
	}
	if any {
		out = append(out, cur.String())
	}
	return out
}
