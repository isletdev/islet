package vpn

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
)

// Apply makes the host match the rows: the interface, its peers, the access
// table and the firewall openings. It is idempotent, and it is the only thing
// that changes any of them, so there is one place to read to know what the
// VPN does to a machine.
func (s *Service) Apply(ctx context.Context, actor string) error {
	// A request that asked for this may be gone before it finishes — the
	// panel reached over the very tunnel being changed is the obvious case —
	// and half an apply is worse than either end of one.
	ctx = context.WithoutCancel(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.Config(ctx)
	if !c.Enabled {
		return s.teardown(ctx, actor)
	}
	if err := s.ensureTools(ctx, actor); err != nil {
		return err
	}
	priv, err := s.privateKey(ctx)
	if err != nil {
		return err
	}
	peers, err := s.Peers(ctx)
	if err != nil {
		return err
	}
	live := []Peer{}
	pre := map[string]string{}
	for _, p := range peers {
		if p.Expired {
			continue
		}
		k, err := s.psk(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("could not read the preshared key of %s: %w", p.Name, err)
		}
		pre[p.ID] = k
		live = append(live, p)
	}
	prefix, _ := netip.ParsePrefix(c.Subnet)

	if _, err := s.run.Read(ctx, "ip", "link", "show", "dev", Iface); err != nil {
		if _, err := s.sh(ctx, actor, nil, "ip", "link", "add", "dev", Iface, "type", "wireguard"); err != nil {
			return fmt.Errorf("could not create the WireGuard interface (is the wireguard kernel module available?): %w", err)
		}
	}
	// syncconf, not setconf. Both leave exactly the peers given, but setconf
	// gets there by removing every peer and adding them back, which ends every
	// session: adding one phone disconnected every other device, measured, and
	// threw away the handshake times the panel shows. syncconf changes only
	// what differs. The configuration travels on stdin, so the keys in it
	// never appear in a process list or in the command log.
	if _, err := s.sh(ctx, actor, []byte(serverConf(priv, c.Port, live, pre)), "wg", "syncconf", Iface, "/dev/stdin"); err != nil {
		return err
	}
	if _, err := s.sh(ctx, actor, nil, "ip", "address", "replace", c.Address()+"/"+strconv.Itoa(prefix.Bits()), "dev", Iface); err != nil {
		return err
	}
	if _, err := s.sh(ctx, actor, nil, "ip", "link", "set", "dev", Iface, "mtu", "1420", "up"); err != nil {
		return err
	}
	if _, err := s.sh(ctx, actor, []byte(Ruleset(c, live)), "nft", "-f", "/dev/stdin"); err != nil {
		return fmt.Errorf("could not load the access rules: %w", err)
	}
	s.forwarding(ctx, actor)
	if err := s.openFirewall(ctx, actor, c.Port); err != nil {
		return err
	}
	s.watchMu.Lock()
	s.active = activeKey(live)
	s.watchMu.Unlock()
	return nil
}

// teardown removes everything Apply adds. Each step tolerates the thing
// already being gone, because a half-done teardown has to be finishable.
func (s *Service) teardown(ctx context.Context, actor string) error {
	if _, err := s.run.Read(ctx, "ip", "link", "show", "dev", Iface); err == nil {
		if _, err := s.sh(ctx, actor, nil, "ip", "link", "del", "dev", Iface); err != nil {
			return err
		}
	}
	if has("nft") {
		if _, err := s.run.Read(ctx, "nft", "list", "table", "inet", table); err == nil {
			if _, err := s.sh(ctx, actor, nil, "nft", "delete", "table", "inet", table); err != nil {
				return err
			}
		}
	}
	s.closeFirewall(ctx, actor)
	s.watchMu.Lock()
	s.active = ""
	s.watchMu.Unlock()
	return nil
}

func has(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

func (s *Service) ensureTools(ctx context.Context, actor string) error {
	if has("wg") && has("nft") && has("ip") {
		return nil
	}
	if !has("apt-get") {
		return errors.New("needs wireguard-tools, nftables and iproute2; install them with your package manager")
	}
	_, err := s.sh(ctx, actor, nil, "sh", "-c", "DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq wireguard-tools nftables iproute2")
	return err
}

// sh runs a recorded command and folds its stderr into the error, which is
// where the reason a kernel or a firewall refused something is.
func (s *Service) sh(ctx context.Context, actor string, stdin []byte, name string, args ...string) (string, error) {
	var res cmdrun.Result
	var err error
	if stdin != nil {
		res, err = s.run.RunInput(ctx, actor, stdin, name, args...)
	} else {
		res, err = s.run.Run(ctx, actor, name, args...)
	}
	if err != nil {
		var ce *cmdrun.Error
		if errors.As(err, &ce) {
			return res.Stdout, fmt.Errorf("%s: %s", name, strings.TrimSpace(ce.Result.Stderr+" "+ce.Result.Stdout))
		}
		return "", err
	}
	return res.Stdout, nil
}

// serverConf is the wg(8) configuration of the interface.
func serverConf(priv string, port int, peers []Peer, psk map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nListenPort = %d\n", priv, port)
	for _, p := range peers {
		fmt.Fprintf(&b, "\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s/32\n", p.PublicKey, psk[p.ID], p.Address)
	}
	return b.String()
}

// privateRanges are what an internet-only peer may not reach through the
// server: the server's own networks, its containers, and anything else that
// is not the internet.
const privateRanges = "10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16"

// Ruleset is the nftables table that enforces each peer's access level.
//
// It is a table of its own, so it is loaded and removed whole and never
// edits anybody else's. It only ever drops: an accept in one nftables table
// does not overrule a drop in another, so the openings that let traffic in at
// all are the ufw rules in openFirewall, and this narrows them per peer. A
// drop here is final whatever ufw or Docker think.
//
// The first statement creates the table when it is missing, so the delete
// after it never fails; the three together replace the table in one
// transaction, with no moment where the old rules are gone and the new ones
// not yet there.
func Ruleset(c Config, peers []Peer) string {
	var admin, internet, apps []string
	for _, p := range peers {
		switch p.Access {
		case AccessAdmin:
			admin = append(admin, p.Address)
		case AccessApps:
			for _, port := range p.Ports {
				apps = append(apps, fmt.Sprintf("%s . %d", p.Address, port))
			}
			if p.FullTunnel {
				internet = append(internet, p.Address)
			}
		case AccessInternet:
			internet = append(internet, p.Address)
		}
	}
	set := func(name, typ string, el []string) string {
		if len(el) == 0 {
			return fmt.Sprintf("\tset %s { type %s; }\n", name, typ)
		}
		return fmt.Sprintf("\tset %s { type %s; elements = { %s } }\n", name, typ, strings.Join(el, ", "))
	}
	srv := c.Address()
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\ntable inet %s {\n", table, table, table)
	b.WriteString(set("admin", "ipv4_addr", admin))
	b.WriteString(set("internet", "ipv4_addr", internet))
	b.WriteString(set("apps", "ipv4_addr . inet_service", apps))
	fmt.Fprintf(&b, "\tset private { type ipv4_addr; flags interval; elements = { %s } }\n", privateRanges)
	fmt.Fprintf(&b, `	chain input {
		type filter hook input priority filter - 10; policy accept;
		iifname != "%[1]s" accept
		ct state established,related accept
		ip saddr @admin accept
		meta l4proto { tcp, udp } ip saddr . th dport @apps accept
		icmp type echo-request limit rate 5/second accept
		drop
	}
	chain forward {
		type filter hook forward priority filter - 10; policy accept;
		iifname != "%[1]s" accept
		ct state established,related accept
		oifname "%[1]s" drop
		ip saddr @admin accept
		ct status dnat ct original ip daddr %[2]s meta l4proto { tcp, udp } ip saddr . ct original proto-dst @apps accept
		ct status dnat ct original ip daddr %[2]s drop
		ip saddr @internet ip daddr != @private accept
		drop
	}
	chain postrouting {
		type nat hook postrouting priority srcnat + 10; policy accept;
		ip saddr %[3]s oifname != "%[1]s" masquerade
	}
}
`, Iface, srv, c.Subnet)
	return b.String()
}

// forwarding turns on IPv4 forwarding when it is off. Docker turns it on
// already on nearly every server this runs on; this is for the one where it
// did not, and it changes the running kernel rather than a file in /etc.
func (s *Service) forwarding(ctx context.Context, actor string) {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err == nil && strings.TrimSpace(string(b)) == "1" {
		return
	}
	_, _ = s.sh(ctx, actor, nil, "sysctl", "-w", "net.ipv4.ip_forward=1")
}

// ---- the firewall ----

const (
	keyAppliedPort = "vpn.applied_port"
	ufwComment     = "Islet VPN"
)

func (s *Service) ufwActive(ctx context.Context) bool {
	if !has("ufw") {
		return false
	}
	res, err := s.run.Read(ctx, "ufw", "status")
	return err == nil && strings.Contains(res.Stdout, "Status: active")
}

// openFirewall lets the tunnel's UDP port in, and lets traffic arriving
// through the tunnel in and through. The access table then narrows that per
// peer.
//
// With ufw active these are ufw rules, so they appear on the Security page
// next to every other opening and a person can see why the port is open.
// Without it, the INPUT policy is already accept and only Docker's FORWARD
// policy of drop stands in the way, which needs two iptables rules.
func (s *Service) openFirewall(ctx context.Context, actor string, port int) error {
	prev, _, _ := s.st.Setting(ctx, keyAppliedPort)
	if s.ufwActive(ctx) {
		if prev != "" && prev != strconv.Itoa(port) {
			_, _ = s.sh(ctx, actor, nil, "ufw", "delete", "allow", prev+"/udp")
		}
		for _, args := range [][]string{
			{"allow", strconv.Itoa(port) + "/udp", "comment", ufwComment},
			{"allow", "in", "on", Iface, "comment", ufwComment + " peers"},
			{"route", "allow", "in", "on", Iface, "comment", ufwComment + " peers"},
		} {
			if _, err := s.sh(ctx, actor, nil, "ufw", args...); err != nil {
				return err
			}
		}
	} else if has("iptables") {
		for _, rule := range [][]string{
			{"FORWARD", "-i", Iface, "-j", "ACCEPT"},
			{"FORWARD", "-o", Iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		} {
			if _, err := s.run.Read(ctx, "iptables", append([]string{"-C"}, rule...)...); err != nil {
				if _, err := s.sh(ctx, actor, nil, "iptables", append([]string{"-I"}, rule...)...); err != nil {
					return err
				}
			}
		}
	}
	return s.st.SetSetting(ctx, keyAppliedPort, strconv.Itoa(port))
}

func (s *Service) closeFirewall(ctx context.Context, actor string) {
	prev, _, _ := s.st.Setting(ctx, keyAppliedPort)
	if prev == "" {
		return
	}
	if s.ufwActive(ctx) {
		_, _ = s.sh(ctx, actor, nil, "ufw", "delete", "allow", prev+"/udp")
		_, _ = s.sh(ctx, actor, nil, "ufw", "delete", "allow", "in", "on", Iface)
		_, _ = s.sh(ctx, actor, nil, "ufw", "route", "delete", "allow", "in", "on", Iface)
	}
	if has("iptables") {
		for _, rule := range [][]string{
			{"FORWARD", "-i", Iface, "-j", "ACCEPT"},
			{"FORWARD", "-o", Iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		} {
			if _, err := s.run.Read(ctx, "iptables", append([]string{"-C"}, rule...)...); err == nil {
				_, _ = s.sh(ctx, actor, nil, "iptables", append([]string{"-D"}, rule...)...)
			}
		}
	}
	_ = s.st.SetSetting(ctx, keyAppliedPort, "")
}

// ---- what WireGuard says ----

// Live is what the kernel reports for one peer.
type Live struct {
	Handshake time.Time
	Endpoint  string
	Rx, Tx    int64
}

// online is how recent a handshake has to be. WireGuard rekeys every two
// minutes while traffic flows, and a device with keepalive on shakes hands at
// least that often; three minutes leaves room for one to be late.
const online = 3 * time.Minute

// live reads handshakes, endpoints and transfer for an interface. It uses
// three narrow queries rather than `wg show dump`, which prints the private
// and preshared keys alongside — this way no key is ever in a buffer that an
// error message could carry somewhere.
func (s *Service) live(ctx context.Context, iface string) (map[string]*Live, error) {
	out := map[string]*Live{}
	get := func(k string) *Live {
		if out[k] == nil {
			out[k] = &Live{}
		}
		return out[k]
	}
	res, err := s.run.Read(ctx, "wg", "show", iface, "latest-handshakes")
	if err != nil {
		return nil, err
	}
	for _, f := range fieldLines(res.Stdout, 2) {
		if n, err := strconv.ParseInt(f[1], 10, 64); err == nil && n > 0 {
			get(f[0]).Handshake = time.Unix(n, 0)
		} else {
			get(f[0])
		}
	}
	if res, err := s.run.Read(ctx, "wg", "show", iface, "endpoints"); err == nil {
		for _, f := range fieldLines(res.Stdout, 2) {
			if f[1] != "(none)" {
				get(f[0]).Endpoint = f[1]
			}
		}
	}
	if res, err := s.run.Read(ctx, "wg", "show", iface, "transfer"); err == nil {
		for _, f := range fieldLines(res.Stdout, 3) {
			l := get(f[0])
			l.Rx, _ = strconv.ParseInt(f[1], 10, 64)
			l.Tx, _ = strconv.ParseInt(f[2], 10, 64)
		}
	}
	return out, nil
}

func fieldLines(s string, n int) [][]string {
	var out [][]string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) >= n {
			out = append(out, f)
		}
	}
	return out
}

// Status is the page: the configuration, every peer with what WireGuard says
// about it, the lockdown and the uplink.
type Status struct {
	Config    Config     `json:"config"`
	Address   string     `json:"address"`
	Up        bool       `json:"up"`
	Peers     []Peer     `json:"peers"`
	Lockdown  Lockdown   `json:"lockdown"`
	Uplink    *UplinkOut `json:"uplink,omitempty"`
	Supported bool       `json:"supported"`
	Note      string     `json:"note,omitempty"`
}

// Status reads it all.
func (s *Service) Status(ctx context.Context) (Status, error) {
	c := s.Config(ctx)
	st := Status{Config: c, Address: c.Address(), Lockdown: s.Lockdown(), Supported: true}
	if _, err := os.Stat("/sys/module/wireguard"); err != nil {
		if _, err := os.Stat("/proc/sys/net"); err != nil {
			st.Supported, st.Note = false, "WireGuard needs a Linux kernel."
		}
	}
	peers, err := s.Peers(ctx)
	if err != nil {
		return st, err
	}
	if c.Enabled {
		if live, err := s.live(ctx, Iface); err == nil {
			st.Up = true
			now := s.now()
			for i := range peers {
				l := live[peers[i].PublicKey]
				if l == nil {
					continue
				}
				peers[i].Endpoint = l.Endpoint
				peers[i].RxBytes, peers[i].TxBytes = l.Rx, l.Tx
				if !l.Handshake.IsZero() {
					peers[i].LastHandshake = l.Handshake.UTC().Format(time.RFC3339)
					peers[i].Online = now.Sub(l.Handshake) < online
				}
			}
		}
	}
	st.Peers = peers
	st.Uplink = s.uplinkStatus(ctx)
	return st, nil
}

// activeKey identifies the set of peers that should be in the interface, so
// the watcher can tell when an expiry has changed it.
func activeKey(peers []Peer) string {
	ids := make([]string, 0, len(peers))
	for _, p := range peers {
		ids = append(ids, p.ID)
	}
	return strings.Join(ids, ",")
}
