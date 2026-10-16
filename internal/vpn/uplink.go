package vpn

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// The uplink is the other end of fleet-over-VPN: a managed server dialling
// its controller's VPN as a peer, so the controller can reach it on a tunnel
// address and its SSH need not be open to the internet.
//
// The member makes its own key, and only the public half leaves it. The
// controller asks for that, adds the peer on its side, and sends back where
// to connect. Nothing about this needs the member to run a VPN server of its
// own, and a member may run one as well — the two are separate interfaces.

// Uplink is what the member stores.
type Uplink struct {
	Endpoint      string `json:"endpoint"`
	ServerKey     string `json:"serverPublicKey"`
	Address       string `json:"address"`
	ServerAddress string `json:"serverAddress"`
	PublicKey     string `json:"publicKey"`
	// Both encrypted, hex.
	PrivateEnc string `json:"privateEnc"`
	PSKEnc     string `json:"pskEnc,omitempty"`
}

// UplinkIn is what the controller sends.
type UplinkIn struct {
	Endpoint      string `json:"endpoint"`
	ServerKey     string `json:"serverPublicKey"`
	PresharedKey  string `json:"presharedKey"`
	Address       string `json:"address"`
	ServerAddress string `json:"serverAddress"`
}

// UplinkOut is what the panel shows.
type UplinkOut struct {
	Endpoint      string `json:"endpoint"`
	Address       string `json:"address"`
	ServerAddress string `json:"serverAddress"`
	Online        bool   `json:"online"`
	LastHandshake string `json:"lastHandshake,omitempty"`
}

const keyUplink = "vpn.uplink"

func (s *Service) loadUplink(ctx context.Context) (Uplink, bool) {
	var u Uplink
	v, _, _ := s.st.Setting(ctx, keyUplink)
	if v == "" || json.Unmarshal([]byte(v), &u) != nil {
		return u, false
	}
	return u, true
}

func (s *Service) saveUplink(ctx context.Context, u *Uplink) error {
	if u == nil {
		return s.st.SetSetting(ctx, keyUplink, "")
	}
	b, _ := json.Marshal(u)
	return s.st.SetSetting(ctx, keyUplink, string(b))
}

// UplinkKey makes this server's key for joining a controller's VPN and
// returns the public half. A key made earlier and not yet used is replaced:
// it was never sent anywhere that matters.
func (s *Service) UplinkKey(ctx context.Context, actor string) (string, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	enc, err := s.sealRaw(priv.Bytes())
	if err != nil {
		return "", err
	}
	u, _ := s.loadUplink(ctx)
	if u.Endpoint != "" {
		return "", errors.New("this server is already on a controller's VPN; leave it first")
	}
	u = Uplink{PrivateEnc: enc, PublicKey: b64(priv.PublicKey().Bytes())}
	if err := s.saveUplink(ctx, &u); err != nil {
		return "", err
	}
	_ = s.st.Audit(ctx, actor, "vpn.uplink.key", "", "")
	return u.PublicKey, nil
}

// JoinUplink connects to the controller.
func (s *Service) JoinUplink(ctx context.Context, actor string, in UplinkIn) error {
	u, ok := s.loadUplink(ctx)
	if !ok || u.PrivateEnc == "" {
		return errors.New("ask for this server's key first")
	}
	if _, err := ParseKey(in.ServerKey); err != nil {
		return err
	}
	if b, err := decodeB64(in.PresharedKey); err != nil || len(b) != 32 {
		return errors.New("the preshared key is not 32 bytes of base64")
	}
	host, port, err := net.SplitHostPort(in.Endpoint)
	if err != nil || host == "" || port == "" {
		return errors.New("the endpoint must be host:port")
	}
	for _, a := range []string{in.Address, in.ServerAddress} {
		if ip, err := netip.ParseAddr(a); err != nil || !ip.Is4() || !ip.IsPrivate() {
			return errors.New("the tunnel addresses must be private IPv4 addresses")
		}
	}
	raw, _ := decodeB64(in.PresharedKey)
	pskEnc, err := s.sealRaw(raw)
	if err != nil {
		return err
	}
	u.Endpoint, u.ServerKey, u.Address, u.ServerAddress, u.PSKEnc = in.Endpoint, in.ServerKey, in.Address, in.ServerAddress, pskEnc
	if err := s.saveUplink(ctx, &u); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "vpn.uplink.join", in.Endpoint, in.Address)
	return s.applyUplink(ctx, actor)
}

// LeaveUplink takes the interface down and forgets the key.
func (s *Service) LeaveUplink(ctx context.Context, actor string) error {
	if err := s.saveUplink(ctx, nil); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "vpn.uplink.leave", "", "")
	return s.applyUplink(ctx, actor)
}

func (s *Service) applyUplink(ctx context.Context, actor string) error {
	ctx = context.WithoutCancel(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.loadUplink(ctx)
	if !ok || u.Endpoint == "" {
		if _, err := s.run.Read(ctx, "ip", "link", "show", "dev", UplinkIface); err == nil {
			if _, err := s.sh(ctx, actor, nil, "ip", "link", "del", "dev", UplinkIface); err != nil {
				return err
			}
		}
		if s.ufwActive(ctx) {
			_, _ = s.sh(ctx, actor, nil, "ufw", "delete", "allow", "in", "on", UplinkIface)
		}
		return nil
	}
	if err := s.ensureTools(ctx, actor); err != nil {
		return err
	}
	priv, err := s.openB64(u.PrivateEnc)
	if err != nil {
		return err
	}
	psk, err := s.openB64(u.PSKEnc)
	if err != nil {
		return err
	}
	if _, err := s.run.Read(ctx, "ip", "link", "show", "dev", UplinkIface); err != nil {
		if _, err := s.sh(ctx, actor, nil, "ip", "link", "add", "dev", UplinkIface, "type", "wireguard"); err != nil {
			return err
		}
	}
	// Only the controller's own address is routed into the tunnel, so the
	// controller is the one thing this server reaches through it, and
	// nothing the controller's other peers send can arrive here.
	conf := fmt.Sprintf("[Interface]\nPrivateKey = %s\n\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nEndpoint = %s\nAllowedIPs = %s/32\nPersistentKeepalive = 25\n",
		priv, u.ServerKey, psk, u.Endpoint, u.ServerAddress)
	if _, err := s.sh(ctx, actor, []byte(conf), "wg", "syncconf", UplinkIface, "/dev/stdin"); err != nil {
		return err
	}
	if _, err := s.sh(ctx, actor, nil, "ip", "address", "replace", u.Address+"/32", "peer", u.ServerAddress+"/32", "dev", UplinkIface); err != nil {
		return err
	}
	if _, err := s.sh(ctx, actor, nil, "ip", "link", "set", "dev", UplinkIface, "mtu", "1420", "up"); err != nil {
		return err
	}
	if s.ufwActive(ctx) {
		if _, err := s.sh(ctx, actor, nil, "ufw", "allow", "in", "on", UplinkIface, "comment", "Islet VPN controller"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) uplinkStatus(ctx context.Context) *UplinkOut {
	u, ok := s.loadUplink(ctx)
	if !ok || u.Endpoint == "" {
		return nil
	}
	out := &UplinkOut{Endpoint: u.Endpoint, Address: u.Address, ServerAddress: u.ServerAddress}
	if live, err := s.live(ctx, UplinkIface); err == nil {
		if l := live[u.ServerKey]; l != nil && !l.Handshake.IsZero() {
			out.LastHandshake = l.Handshake.UTC().Format(time.RFC3339)
			out.Online = s.now().Sub(l.Handshake) < online
		}
	}
	return out
}

// Handshake reports when a peer last completed a handshake with this
// server, for the controller waiting on a fleet server it just added.
func (s *Service) Handshake(ctx context.Context, publicKey string) time.Time {
	live, err := s.live(ctx, Iface)
	if err != nil || live[publicKey] == nil {
		return time.Time{}
	}
	return live[publicKey].Handshake
}

func decodeB64(v string) ([]byte, error) {
	return b64dec(strings.TrimSpace(v))
}
