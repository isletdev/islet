package vpn

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/store"
)

// plainKeys stands in for the daemon's encryption.
type plainKeys struct{}

func (plainKeys) Encrypt(b []byte) ([]byte, error) { return append([]byte("k:"), b...), nil }
func (plainKeys) Decrypt(b []byte) ([]byte, error) { return b[2:], nil }

func pubKey(t *testing.T) string {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return b64(k.PublicKey().Bytes())
}

// fakeHost puts an ip, wg, nft and ufw on PATH that record their arguments
// and stdin, and answer the way a host with ufw active and no interface yet
// does. Nothing real is touched.
func fakeHost(t *testing.T) (calls func() string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	added := filepath.Join(bin, "added")
	_ = os.WriteFile(added, []byte("Added user rules (see 'ufw status' for running firewall):\nufw limit 22/tcp comment 'SSH'\nufw allow 80/tcp comment 'HTTP'\nufw allow 9443/tcp comment 'Islet panel'\nufw allow from 203.0.113.9 to any port 22 proto tcp\n"), 0o644)
	script := func(body string) string {
		return "#!/bin/sh\necho \"$(basename $0) $*\" >> " + log + "\n" + body + "\nexit 0\n"
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script(body)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("ip", `case "$*" in "link show dev"*) exit 1;; esac`)
	write("wg", `case "$*" in *syncconf*) sed 's/^/  stdin: /' >> `+log+`;; esac`)
	write("nft", `case "$*" in *"-f /dev/stdin"*) cat > `+filepath.Join(bin, "ruleset")+`;; "list table"*) exit 1;; esac`)
	write("ufw", `case "$*" in "status") echo "Status: active";; "show added") cat `+added+`;; esac`)
	write("sysctl", ``)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string { b, _ := os.ReadFile(log); return string(b) }
}

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, cmdrun.New(st, slog.New(slog.NewTextHandler(io.Discard, nil))), plainKeys{}, nil, Hooks{})
}

func enable(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.Enable(context.Background(), "test", Config{Endpoint: "198.51.100.7"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
}

// A device is added with a public key only, and the preshared key that comes
// back is the one in the interface — and in nothing that gets logged.
func TestAddPeerConfiguresTheInterface(t *testing.T) {
	calls := fakeHost(t)
	s := newService(t)
	enable(t, s)
	key := pubKey(t)
	iss, err := s.AddPeer(context.Background(), "test", PeerInput{Name: "phone", PublicKey: key}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if iss.Peer.Address != "10.66.0.2" || iss.Peer.Access != AccessAdmin {
		t.Fatalf("got %+v", iss.Peer)
	}
	if got := strings.Join(iss.AllowedIPs, ","); got != "10.66.0.1/32" {
		t.Errorf("a split tunnel routes only the server, got %s", got)
	}
	if iss.Endpoint != "198.51.100.7:51820" {
		t.Errorf("endpoint %s", iss.Endpoint)
	}
	log := calls()
	for _, want := range []string{
		"ip link add dev wg-islet type wireguard",
		"PublicKey = " + key,
		"PresharedKey = " + iss.PresharedKey,
		"AllowedIPs = 10.66.0.2/32",
		"ip address replace 10.66.0.1/24 dev wg-islet",
		"ufw allow 51820/udp comment Islet VPN",
		"ufw allow in on wg-islet",
		"ufw route allow in on wg-islet",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("never saw %q in:\n%s", want, log)
		}
	}
	// What the command log keeps is name and arguments; the keys went on
	// stdin, so they must not be in it.
	var shown string
	_ = s.st.DB.QueryRow(`SELECT group_concat(command, char(10)) FROM commands`).Scan(&shown)
	if strings.Contains(shown, iss.PresharedKey) {
		t.Errorf("the preshared key reached the command log:\n%s", shown)
	}
}

func TestFullTunnelAndAddresses(t *testing.T) {
	fakeHost(t)
	s := newService(t)
	enable(t, s)
	ctx := context.Background()
	a, err := s.AddPeer(ctx, "test", PeerInput{Name: "laptop", PublicKey: pubKey(t), Access: AccessInternet}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Peer.FullTunnel || strings.Join(a.AllowedIPs, ",") != "0.0.0.0/0,::/0" || a.DNS != DefaultDNS {
		t.Errorf("internet access is a full tunnel with DNS: %+v", a)
	}
	b, err := s.AddPeer(ctx, "test", PeerInput{Name: "tablet", PublicKey: pubKey(t), Access: AccessApps}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Peer.Address != "10.66.0.3" || strings.Join([]string{itoa(b.Peer.Ports)}, "") != "80,443" {
		t.Errorf("apps defaults to 80 and 443 on the next address: %+v", b.Peer)
	}
	if err := s.RemovePeer(ctx, "test", a.Peer.ID); err != nil {
		t.Fatal(err)
	}
	c, _ := s.AddPeer(ctx, "test", PeerInput{Name: "watch", PublicKey: pubKey(t)}, "", "")
	if c.Peer.Address != "10.66.0.2" {
		t.Errorf("a freed address is reused, got %s", c.Peer.Address)
	}
	if _, err := s.AddPeer(ctx, "test", PeerInput{Name: "watch", PublicKey: pubKey(t)}, "", ""); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("a duplicate name is refused by name, got %v", err)
	}
	if _, err := s.AddPeer(ctx, "test", PeerInput{Name: "bad", PublicKey: "not-a-key"}, "", ""); err == nil {
		t.Error("a malformed key is refused")
	}
}

func itoa(ps []int) string { return joinPorts(ps) }

func TestRulesetPutsEachPeerInItsSet(t *testing.T) {
	c := Config{Subnet: "10.66.0.0/24"}
	r := Ruleset(c, []Peer{
		{Address: "10.66.0.2", Access: AccessAdmin},
		{Address: "10.66.0.3", Access: AccessApps, Ports: []int{80, 443}, FullTunnel: true},
		{Address: "10.66.0.4", Access: AccessInternet},
		{Address: "10.66.0.5", Access: AccessServer},
	})
	for _, want := range []string{
		"set admin { type ipv4_addr; elements = { 10.66.0.2 } }",
		"set internet { type ipv4_addr; elements = { 10.66.0.3, 10.66.0.4 } }",
		"elements = { 10.66.0.3 . 80, 10.66.0.3 . 443 }",
		"ct original ip daddr 10.66.0.1",
		"ip saddr 10.66.0.0/24 oifname != \"wg-islet\" masquerade",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("missing %q in\n%s", want, r)
		}
	}
	if strings.Contains(r, "10.66.0.5") {
		t.Error("a fleet server is in no set: it never starts a connection to anything")
	}
	if !strings.HasPrefix(r, "table inet islet_vpn\ndelete table inet islet_vpn\n") {
		t.Error("the table is replaced in one transaction")
	}
}

func TestPublicRules(t *testing.T) {
	added := "Added user rules (see 'ufw status' for running firewall):\n" +
		"ufw limit 22/tcp comment 'SSH'\n" +
		"ufw allow OpenSSH\n" +
		"ufw allow 9443/tcp comment 'Islet panel'\n" +
		"ufw allow from 203.0.113.9 to any port 22 proto tcp\n" +
		"ufw allow in on wg-islet comment 'Islet VPN peers'\n" +
		"ufw allow 2222\n"
	got := PublicRules(added, []int{22, 9443})
	want := []string{"limit 22/tcp comment SSH", "allow OpenSSH", "allow 9443/tcp comment Islet panel"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != want[i] {
			t.Errorf("rule %d: got %q want %q", i, strings.Join(got[i], " "), want[i])
		}
	}
}

// The lockdown opens the ports to the tunnel before it closes them to
// everybody, puts back exactly what it took, and undoes itself when nobody
// confirms from inside.
func TestLockdownRevertsUnconfirmed(t *testing.T) {
	calls := fakeHost(t)
	s := newService(t)
	enable(t, s)
	ctx := context.Background()
	if err := s.SetLockdown(ctx, "test", LockdownInput{Panel: true, SSH: true}, "198.51.100.1"); err == nil {
		t.Fatal("a lockdown with no admin device would lock everybody out")
	}
	if _, err := s.AddPeer(ctx, "test", PeerInput{Name: "phone", PublicKey: pubKey(t)}, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLockdown(ctx, "test", LockdownInput{Panel: true, SSH: true, PanelPort: 9443, SSHPort: 22}, "198.51.100.1"); err != nil {
		t.Fatal(err)
	}
	l := s.Lockdown()
	if !l.Pending || len(l.Removed) != 2 {
		t.Fatalf("lockdown %+v", l)
	}
	log := calls()
	open := strings.Index(log, "ufw allow from 10.66.0.0/24 to any port 22 proto tcp")
	closed := strings.Index(log, "ufw delete limit 22/tcp")
	if open < 0 || closed < 0 || open > closed {
		t.Fatalf("the tunnel must be let in before the public rule goes:\n%s", log)
	}
	if s.Allowed("198.51.100.1") || !s.Allowed("10.66.0.2") || !s.Allowed("127.0.0.1") {
		t.Error("the gate lets in the tunnel and this machine, nothing else")
	}
	if err := s.Confirm(ctx, "test", "198.51.100.1"); err == nil {
		t.Error("a confirmation from outside the tunnel proves nothing")
	}
	// Run the clock out.
	s.lockMu.Lock()
	s.lockTime.Reset(time.Millisecond)
	s.lockMu.Unlock()
	time.Sleep(200 * time.Millisecond)
	if s.Lockdown().Active() {
		t.Fatal("an unconfirmed lockdown must undo itself")
	}
	log = calls()
	for _, want := range []string{"ufw limit 22/tcp comment SSH", "ufw allow 9443/tcp comment Islet panel", "ufw delete allow from 10.66.0.0/24 to any port 9443 proto tcp"} {
		if !strings.Contains(log, want) {
			t.Errorf("undo never ran %q:\n%s", want, log)
		}
	}
}

func TestLockdownConfirmedStays(t *testing.T) {
	fakeHost(t)
	s := newService(t)
	enable(t, s)
	ctx := context.Background()
	_, _ = s.AddPeer(ctx, "test", PeerInput{Name: "phone", PublicKey: pubKey(t)}, "", "")
	if err := s.SetLockdown(ctx, "test", LockdownInput{Panel: true, KeepIP: "198.51.100.1"}, "198.51.100.1"); err != nil {
		t.Fatal(err)
	}
	if !s.Allowed("198.51.100.1") {
		t.Error("the fallback address is let in")
	}
	if err := s.Confirm(ctx, "test", "10.66.0.2"); err != nil {
		t.Fatal(err)
	}
	if l := s.Lockdown(); l.Pending || !l.Panel {
		t.Errorf("confirmed: %+v", l)
	}
	// And the stored copy agrees, which is what a restart reads.
	if l := s.loadLockdown(ctx); l.Pending || !l.Panel {
		t.Errorf("stored: %+v", l)
	}
	if err := s.RemovePeer(ctx, "test", mustPeers(t, s)[0].ID); err == nil {
		t.Error("removing the last admin device under a lockdown is refused")
	}
}

func mustPeers(t *testing.T, s *Service) []Peer {
	ps, err := s.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestExpiredPeerLeavesTheInterface(t *testing.T) {
	calls := fakeHost(t)
	s := newService(t)
	enable(t, s)
	ctx := context.Background()
	key := pubKey(t)
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := s.AddPeer(ctx, "test", PeerInput{Name: "guest", PublicKey: key, ExpiresAt: exp}, "", ""); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	before := strings.Count(calls(), "PublicKey = "+key)
	s.tick(ctx)
	log := calls()
	if strings.Count(log, "PublicKey = "+key) != before {
		t.Error("an expired device was written into the interface again")
	}
	if !strings.Contains(log[len(log)/2:], "wg syncconf wg-islet /dev/stdin") {
		t.Error("an expiry re-applies the interface")
	}
}

func TestWGEasyImportKeepsDevicesWorking(t *testing.T) {
	fakeHost(t)
	s := newService(t)
	srv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	k1, k2 := pubKey(t), pubKey(t)
	psk := b64(make([]byte, 32))
	file := `{"server":{"privateKey":"` + b64(srv.Bytes()) + `","publicKey":"` + b64(srv.PublicKey().Bytes()) + `","address":"10.8.0.1"},
	"clients":{"a":{"name":"phone","address":"10.8.0.2","publicKey":"` + k1 + `","preSharedKey":"` + psk + `","enabled":true},
	"b":{"name":"old","address":"10.8.0.3","publicKey":"` + k2 + `","enabled":false}}}`
	plan, err := s.ImportWGEasy(context.Background(), "test", []byte(file), "198.51.100.7", 51820)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Subnet != "10.8.0.0/24" || len(plan.Devices) != 1 || len(plan.Skipped) != 1 {
		t.Errorf("plan %+v", plan)
	}
	c := s.Config(context.Background())
	if c.PublicKey != b64(srv.PublicKey().Bytes()) || c.Subnet != "10.8.0.0/24" || !c.Enabled {
		t.Errorf("the server key and range are wg-easy's, so devices see no change: %+v", c)
	}
	ps := mustPeers(t, s)
	if len(ps) != 1 || ps[0].Address != "10.8.0.2" || ps[0].Access != AccessInternet {
		t.Errorf("peers %+v", ps)
	}
}

func TestConfigValidation(t *testing.T) {
	for _, c := range []Config{
		{Endpoint: ""},
		{Endpoint: "https://example.com"},
		{Endpoint: "1.2.3.4", Subnet: "8.8.8.0/24"},
		{Endpoint: "1.2.3.4", Subnet: "10.0.0.0/8"},
		{Endpoint: "1.2.3.4", Port: 80},
		{Endpoint: "1.2.3.4", DNS: "dns.google"},
	} {
		if err := c.validate(); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	c := Config{Endpoint: "vpn.example.com", Subnet: "10.66.0.9/24"}
	if err := c.validate(); err != nil || c.Subnet != "10.66.0.0/24" || c.Address() != "10.66.0.1" {
		t.Errorf("%v %+v", err, c)
	}
}
