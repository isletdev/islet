// Package vpn runs a WireGuard server on the host, in the kernel, and decides
// in the firewall what each device on it may reach.
//
// It replaces a catalog app (wg-easy) for the reasons a catalog app could not
// get past. A container with its own web UI has its own password, keeps every
// device's private key in a JSON file, and cannot touch the host firewall —
// so "this phone may open the apps and nothing else" was not something it
// could enforce, and "the panel only over the VPN" was a separate firewall
// rule nobody connected to it.
//
// The shape:
//
//   - A device's private key is generated in the browser that adds it, shown
//     once as a QR code and a file, and never sent here. The server holds
//     public keys and a preshared key per peer, so a copy of the database is
//     not a way into the tunnel.
//   - Each peer has an access level, and the level is a set in an nftables
//     table this package owns: admin reaches everything on the server, apps
//     reaches listed ports, internet only leaves through it, and a fleet
//     server is reached by the controller but never reaches back.
//   - Whether a peer is connected comes from WireGuard, never from a column.
//
// Every command goes through cmdrun, so the drawer shows each one.
package vpn

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

// Iface is the server's interface; UplinkIface is the one a fleet member uses
// to reach its controller. Both fit IFNAMSIZ with room to spare.
const (
	Iface       = "wg-islet"
	UplinkIface = "wg-islet-up"
	table       = "islet_vpn"
)

// Defaults. 10.66.0.0/24 rather than wg-easy's 10.8.0.0/24, so the two can be
// told apart while somebody moves from one to the other.
const (
	DefaultPort   = 51820
	DefaultSubnet = "10.66.0.0/24"
	DefaultDNS    = "1.1.1.1"
)

// Access levels.
const (
	AccessAdmin    = "admin"
	AccessApps     = "apps"
	AccessInternet = "internet"
	AccessServer   = "server"
)

// Keys is the daemon's encryption, an interface so tests need no key file.
type Keys interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

// Config is the server side, as the panel sees it. The private key is never
// part of it.
type Config struct {
	Enabled   bool   `json:"enabled"`
	Endpoint  string `json:"endpoint"`
	Port      int    `json:"port"`
	Subnet    string `json:"subnet"`
	DNS       string `json:"dns"`
	PublicKey string `json:"publicKey"`
}

// Address is the server's own address inside the tunnel: the first host.
func (c Config) Address() string {
	p, err := netip.ParsePrefix(c.Subnet)
	if err != nil {
		return ""
	}
	return p.Masked().Addr().Next().String()
}

// Peer is one device or server.
type Peer struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	PublicKey     string `json:"publicKey"`
	Address       string `json:"address"`
	Access        string `json:"access"`
	Ports         []int  `json:"ports"`
	FullTunnel    bool   `json:"fullTunnel"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	FleetServerID string `json:"fleetServerId,omitempty"`
	FirstSeenAt   string `json:"firstSeenAt,omitempty"`
	CreatedAt     string `json:"createdAt"`
	CreatedBy     string `json:"createdBy,omitempty"`

	// From WireGuard, when the interface is up.
	Expired       bool   `json:"expired"`
	Online        bool   `json:"online"`
	Endpoint      string `json:"endpoint,omitempty"`
	LastHandshake string `json:"lastHandshake,omitempty"`
	RxBytes       int64  `json:"rxBytes"`
	TxBytes       int64  `json:"txBytes"`
}

// PeerInput is what the panel sends to add or change a peer.
type PeerInput struct {
	Name       string `json:"name"`
	PublicKey  string `json:"publicKey"`
	Access     string `json:"access"`
	Ports      []int  `json:"ports"`
	FullTunnel bool   `json:"fullTunnel"`
	ExpiresAt  string `json:"expiresAt"`
}

// Issued is what a new or re-keyed peer needs to build its configuration. The
// preshared key is in it, which is why it is returned once and never again.
type Issued struct {
	Peer         Peer     `json:"peer"`
	PresharedKey string   `json:"presharedKey"`
	ServerKey    string   `json:"serverPublicKey"`
	Endpoint     string   `json:"endpoint"`
	AllowedIPs   []string `json:"allowedIps"`
	DNS          string   `json:"dns,omitempty"`
}

// Hooks are what this package asks of the rest of the daemon without
// importing it.
type Hooks struct {
	// Country names where an address is, or "" when lookups are off. It sends
	// the address to a third party, so the daemon only wires it when the
	// operator has allowed that for logins.
	Country func(ctx context.Context, ip string) string
	// PanelPort is the port the daemon listens on, which the lockdown closes.
	PanelPort func() string
}

// Service owns the interface, the table and the rows.
type Service struct {
	st    *store.Store
	run   *cmdrun.Runner
	keys  Keys
	bus   *notify.Bus
	hooks Hooks
	now   func() time.Time

	// mu serialises everything that changes the interface or the firewall.
	// Two applies interleaving is how a peer ends up in WireGuard and not in
	// the table, or the other way round.
	mu sync.Mutex

	lockMu   sync.Mutex
	lock     Lockdown
	lockTime *time.Timer

	watchMu sync.Mutex
	online  map[string]bool
	active  string
}

// New builds the service. bus may be nil in tests.
func New(st *store.Store, run *cmdrun.Runner, keys Keys, bus *notify.Bus, hooks Hooks) *Service {
	return &Service{st: st, run: run, keys: keys, bus: bus, hooks: hooks, now: time.Now, online: map[string]bool{}}
}

// SetCountry wires the address lookup. It is set by the API layer, which owns
// the setting that allows it, before Start.
func (s *Service) SetCountry(f func(ctx context.Context, ip string) string) { s.hooks.Country = f }

// PanelPort is the port the daemon listens on, which is where a device on
// the VPN opens the panel.
func (s *Service) PanelPort() string {
	if s.hooks.PanelPort != nil {
		if p := s.hooks.PanelPort(); p != "" {
			return p
		}
	}
	return "9443"
}

// ---- configuration ----

const (
	keyConfig  = "vpn.config"
	keyPrivate = "vpn.key"
)

// Config returns the server side.
func (s *Service) Config(ctx context.Context) Config {
	c := Config{Port: DefaultPort, Subnet: DefaultSubnet, DNS: DefaultDNS}
	if v, _, _ := s.st.Setting(ctx, keyConfig); v != "" {
		_ = json.Unmarshal([]byte(v), &c)
	}
	return c
}

func (s *Service) saveConfig(ctx context.Context, c Config) error {
	b, _ := json.Marshal(c)
	return s.st.SetSetting(ctx, keyConfig, string(b))
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// validate checks what a person typed and fills in what they left out.
func (c *Config) validate() error {
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	if c.Endpoint == "" {
		return errors.New("give the address devices connect to: this server's public IP, or a DNS name that points at it without a CDN in front")
	}
	if ip := net.ParseIP(c.Endpoint); ip == nil && !hostRe.MatchString(c.Endpoint) {
		return errors.New("the endpoint must be an IP address or a host name, without a port or a scheme")
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.Port < 1024 || c.Port > 65535 {
		return errors.New("the port must be between 1024 and 65535")
	}
	if c.Subnet == "" {
		c.Subnet = DefaultSubnet
	}
	p, err := netip.ParsePrefix(c.Subnet)
	if err != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p.Bits() < 16 || p.Bits() > 29 {
		return errors.New("the address range must be a private IPv4 range between /16 and /29, like 10.66.0.0/24")
	}
	c.Subnet = p.Masked().String()
	c.DNS = strings.TrimSpace(c.DNS)
	if c.DNS == "" {
		c.DNS = DefaultDNS
	}
	for _, d := range strings.Split(c.DNS, ",") {
		if net.ParseIP(strings.TrimSpace(d)) == nil {
			return errors.New("DNS must be one or more IP addresses, separated by commas")
		}
	}
	return nil
}

// Enable turns the server on, or changes its settings when it already is.
// The key is generated here the first time and kept until Reset.
func (s *Service) Enable(ctx context.Context, actor string, in Config) (Config, error) {
	if err := in.validate(); err != nil {
		return Config{}, err
	}
	cur := s.Config(ctx)
	if cur.Subnet != in.Subnet {
		var n int
		_ = s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM vpn_peers WHERE server_id = ?`, s.st.ServerID).Scan(&n)
		if n > 0 {
			return Config{}, errors.New("the address range cannot change while devices are on it; remove them first")
		}
	}
	if cur.PublicKey == "" {
		priv, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return Config{}, err
		}
		if err := s.saveKey(ctx, priv.Bytes()); err != nil {
			return Config{}, err
		}
		cur.PublicKey = b64(priv.PublicKey().Bytes())
	}
	in.Enabled = true
	in.PublicKey = cur.PublicKey
	if err := s.saveConfig(ctx, in); err != nil {
		return Config{}, err
	}
	_ = s.st.Audit(ctx, actor, "vpn.enable", in.Endpoint, fmt.Sprintf("port %d, %s", in.Port, in.Subnet))
	return in, s.Apply(ctx, actor)
}

// Disable takes the interface and its rules down. Peers and the key stay, so
// turning it back on brings every device back without touching them. The
// lockdown goes first: a panel reachable only over a VPN that is off is not
// reachable.
func (s *Service) Disable(ctx context.Context, actor string) error {
	if s.Lockdown().Active() {
		if err := s.SetLockdown(ctx, actor, LockdownInput{}, ""); err != nil {
			return err
		}
	}
	c := s.Config(ctx)
	c.Enabled = false
	if err := s.saveConfig(ctx, c); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "vpn.disable", "", "")
	return s.Apply(ctx, actor)
}

func (s *Service) saveKey(ctx context.Context, raw []byte) error {
	enc, err := s.keys.Encrypt(raw)
	if err != nil {
		return err
	}
	return s.st.SetSetting(ctx, keyPrivate, hex.EncodeToString(enc))
}

func (s *Service) privateKey(ctx context.Context) (string, error) {
	v, _, _ := s.st.Setting(ctx, keyPrivate)
	if v == "" {
		return "", errors.New("the VPN has no key yet")
	}
	return s.openB64(v)
}

func (s *Service) openB64(hexed string) (string, error) {
	raw, err := hex.DecodeString(hexed)
	if err != nil {
		return "", err
	}
	b, err := s.keys.Decrypt(raw)
	if err != nil {
		return "", err
	}
	return b64(b), nil
}

func (s *Service) sealRaw(raw []byte) (string, error) {
	enc, err := s.keys.Encrypt(raw)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(enc), nil
}

// ---- peers ----

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._'()-]{0,59}$`)

// ParseKey checks a WireGuard public key: base64 of 32 bytes that is a point
// on the curve. A typo here would otherwise be a peer that never connects and
// a support question nobody can answer.
func ParseKey(k string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k))
	if err != nil || len(b) != 32 {
		return nil, errors.New("a WireGuard key is 44 characters of base64")
	}
	if _, err := ecdh.X25519().NewPublicKey(b); err != nil {
		return nil, errors.New("that is not a usable WireGuard key")
	}
	return b, nil
}

func (in *PeerInput) validate(kind string) error {
	in.Name = strings.TrimSpace(in.Name)
	if !nameRe.MatchString(in.Name) {
		return errors.New("a name is 1 to 60 letters, digits, spaces and . _ ' ( ) -")
	}
	if kind == "server" {
		in.Access = AccessServer
	}
	switch in.Access {
	case "":
		in.Access = AccessAdmin
	case AccessAdmin, AccessApps, AccessInternet:
	case AccessServer:
		if kind != "server" {
			return errors.New("the server level is for fleet servers")
		}
	default:
		return errors.New("access is admin, apps or internet")
	}
	if in.Access == AccessInternet {
		in.FullTunnel = true
	}
	if in.Access == AccessServer {
		in.FullTunnel = false
	}
	if in.Access != AccessApps {
		in.Ports = nil
	}
	if in.Access == AccessApps {
		if len(in.Ports) == 0 {
			in.Ports = []int{80, 443}
		}
		if len(in.Ports) > 32 {
			return errors.New("up to 32 ports")
		}
		seen := map[int]bool{}
		out := []int{}
		for _, p := range in.Ports {
			if p < 1 || p > 65535 {
				return fmt.Errorf("%d is not a port", p)
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		sort.Ints(out)
		in.Ports = out
	}
	if in.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, in.ExpiresAt)
		if err != nil {
			return errors.New("the expiry must be a date and time")
		}
		in.ExpiresAt = t.UTC().Format(time.RFC3339)
	}
	return nil
}

const peerCols = `id, name, kind, public_key, address, access, ports, full_tunnel, expires_at,
	fleet_server_id, first_seen_at, created_at, created_by`

func scanPeer(row interface{ Scan(...any) error }) (Peer, error) {
	var p Peer
	var ports string
	var full int
	err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.PublicKey, &p.Address, &p.Access, &ports, &full, &p.ExpiresAt,
		&p.FleetServerID, &p.FirstSeenAt, &p.CreatedAt, &p.CreatedBy)
	p.FullTunnel = full == 1
	p.Ports = []int{}
	for _, f := range strings.Split(ports, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil {
			p.Ports = append(p.Ports, n)
		}
	}
	return p, err
}

// Peers lists every peer from the table alone.
func (s *Service) Peers(ctx context.Context) ([]Peer, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+peerCols+` FROM vpn_peers WHERE server_id = ? ORDER BY kind, name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Peer{}
	now := s.now()
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		p.Expired = expired(p, now)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrNotFound is returned for a peer that is not here.
var ErrNotFound = errors.New("no such device")

// Peer returns one.
func (s *Service) Peer(ctx context.Context, id string) (Peer, error) {
	p, err := scanPeer(s.st.DB.QueryRowContext(ctx, `SELECT `+peerCols+` FROM vpn_peers WHERE server_id = ? AND id = ?`, s.st.ServerID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, ErrNotFound
	}
	p.Expired = expired(p, s.now())
	return p, err
}

func expired(p Peer, now time.Time) bool {
	if p.ExpiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, p.ExpiresAt)
	return err == nil && !now.Before(t)
}

// nextAddress is the lowest free host in the range after the server's own.
func (s *Service) nextAddress(ctx context.Context, c Config) (string, error) {
	p, err := netip.ParsePrefix(c.Subnet)
	if err != nil {
		return "", err
	}
	used := map[string]bool{c.Address(): true}
	rows, err := s.st.DB.QueryContext(ctx, `SELECT address FROM vpn_peers WHERE server_id = ?`, s.st.ServerID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err == nil {
			used[a] = true
		}
	}
	a := p.Masked().Addr().Next()
	for ; p.Contains(a); a = a.Next() {
		if !p.Contains(a.Next()) {
			break // the broadcast address
		}
		if !used[a.String()] {
			return a.String(), nil
		}
	}
	return "", errors.New("the address range is full")
}

// AddPeer stores a peer whose key was made somewhere else, and returns what
// that somewhere needs to finish its configuration.
func (s *Service) AddPeer(ctx context.Context, actor string, in PeerInput, kind, fleetID string) (*Issued, error) {
	if kind == "" {
		kind = "device"
	}
	if err := in.validate(kind); err != nil {
		return nil, err
	}
	if _, err := ParseKey(in.PublicKey); err != nil {
		return nil, err
	}
	c := s.Config(ctx)
	if c.PublicKey == "" {
		return nil, errors.New("turn the VPN on first")
	}
	if strings.TrimSpace(in.PublicKey) == c.PublicKey {
		return nil, errors.New("that is the server's own key")
	}
	addr, err := s.nextAddress(ctx, c)
	if err != nil {
		return nil, err
	}
	psk := make([]byte, 32)
	if _, err := rand.Read(psk); err != nil {
		return nil, err
	}
	pskEnc, err := s.sealRaw(psk)
	if err != nil {
		return nil, err
	}
	id := newID()
	_, err = s.st.DB.ExecContext(ctx, `INSERT INTO vpn_peers
		(id, server_id, name, kind, public_key, psk_enc, address, access, ports, full_tunnel, expires_at, fleet_server_id, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, s.st.ServerID, in.Name, kind, strings.TrimSpace(in.PublicKey), pskEnc, addr, in.Access, joinPorts(in.Ports),
		boolInt(in.FullTunnel), in.ExpiresAt, fleetID, actor)
	if err != nil {
		return nil, uniqueErr(err)
	}
	_ = s.st.Audit(ctx, actor, "vpn.peer.add", in.Name, in.Access+" "+addr)
	if err := s.Apply(ctx, actor); err != nil {
		return nil, err
	}
	p, err := s.Peer(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.issued(c, p, b64(psk)), nil
}

func (s *Service) issued(c Config, p Peer, psk string) *Issued {
	out := &Issued{Peer: p, PresharedKey: psk, ServerKey: c.PublicKey, Endpoint: net.JoinHostPort(c.Endpoint, strconv.Itoa(c.Port))}
	if p.FullTunnel {
		// ::/0 as well, though the server routes no IPv6: a device told to send
		// only IPv4 here would send IPv6 around the tunnel, which is the leak
		// a full tunnel exists to prevent. Sent here, it is dropped instead.
		out.AllowedIPs = []string{"0.0.0.0/0", "::/0"}
		out.DNS = c.DNS
	} else {
		out.AllowedIPs = []string{c.Address() + "/32"}
	}
	return out
}

// UpdatePeer changes what a peer may do. Its key and address stay.
func (s *Service) UpdatePeer(ctx context.Context, actor, id string, in PeerInput) (Peer, error) {
	p, err := s.Peer(ctx, id)
	if err != nil {
		return Peer{}, err
	}
	if err := in.validate(p.Kind); err != nil {
		return Peer{}, err
	}
	_, err = s.st.DB.ExecContext(ctx, `UPDATE vpn_peers SET name = ?, access = ?, ports = ?, full_tunnel = ?, expires_at = ?
		WHERE server_id = ? AND id = ?`, in.Name, in.Access, joinPorts(in.Ports), boolInt(in.FullTunnel), in.ExpiresAt, s.st.ServerID, id)
	if err != nil {
		return Peer{}, uniqueErr(err)
	}
	_ = s.st.Audit(ctx, actor, "vpn.peer.update", in.Name, in.Access)
	if err := s.Apply(ctx, actor); err != nil {
		return Peer{}, err
	}
	return s.Peer(ctx, id)
}

// Rekey replaces a peer's key and preshared key, for a device that was
// reinstalled or a configuration that was shown to the wrong person. The old
// key stops working the moment this returns.
func (s *Service) Rekey(ctx context.Context, actor, id, publicKey string) (*Issued, error) {
	p, err := s.Peer(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := ParseKey(publicKey); err != nil {
		return nil, err
	}
	psk := make([]byte, 32)
	if _, err := rand.Read(psk); err != nil {
		return nil, err
	}
	pskEnc, err := s.sealRaw(psk)
	if err != nil {
		return nil, err
	}
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE vpn_peers SET public_key = ?, psk_enc = ?, first_seen_at = '', countries = ''
		WHERE server_id = ? AND id = ?`, strings.TrimSpace(publicKey), pskEnc, s.st.ServerID, id); err != nil {
		return nil, uniqueErr(err)
	}
	_ = s.st.Audit(ctx, actor, "vpn.peer.rekey", p.Name, "")
	if err := s.Apply(ctx, actor); err != nil {
		return nil, err
	}
	p, err = s.Peer(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.issued(s.Config(ctx), p, b64(psk)), nil
}

// RemovePeer revokes a peer. It is out of WireGuard and the table when this
// returns, and its key is forgotten.
func (s *Service) RemovePeer(ctx context.Context, actor, id string) error {
	p, err := s.Peer(ctx, id)
	if err != nil {
		return err
	}
	if p.Access == AccessAdmin && s.Lockdown().Active() && s.adminPeers(ctx, id) == 0 {
		return errors.New("this is the last admin device and the panel is VPN-only; turn the lockdown off first, or add another admin device")
	}
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM vpn_peers WHERE server_id = ? AND id = ?`, s.st.ServerID, id); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "vpn.peer.remove", p.Name, p.Address)
	return s.Apply(ctx, actor)
}

// adminPeers counts the admin devices other than except that have not
// expired.
func (s *Service) adminPeers(ctx context.Context, except string) int {
	peers, _ := s.Peers(ctx)
	n := 0
	for _, p := range peers {
		if p.ID != except && p.Access == AccessAdmin && !p.Expired {
			n++
		}
	}
	return n
}

// PeerByFleet finds the peer a fleet server joined as.
func (s *Service) PeerByFleet(ctx context.Context, fleetID string) (Peer, error) {
	p, err := scanPeer(s.st.DB.QueryRowContext(ctx, `SELECT `+peerCols+` FROM vpn_peers WHERE server_id = ? AND fleet_server_id = ?`, s.st.ServerID, fleetID))
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, ErrNotFound
	}
	return p, err
}

func (s *Service) psk(ctx context.Context, id string) (string, error) {
	var enc string
	if err := s.st.DB.QueryRowContext(ctx, `SELECT psk_enc FROM vpn_peers WHERE server_id = ? AND id = ?`, s.st.ServerID, id).Scan(&enc); err != nil {
		return "", err
	}
	return s.openB64(enc)
}

// ---- small things ----

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func joinPorts(ps []int) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = strconv.Itoa(p)
	}
	return strings.Join(out, ",")
}

func uniqueErr(err error) error {
	m := err.Error()
	switch {
	case strings.Contains(m, "UNIQUE") && strings.Contains(m, "name"):
		return errors.New("a device with that name is already here")
	case strings.Contains(m, "UNIQUE") && strings.Contains(m, "public_key"):
		return errors.New("that key is already in use by another device")
	}
	return err
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
