package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Moving from wg-easy without touching a single device.
//
// A device's configuration names the server's public key, the endpoint, its
// own address and a preshared key. Keep all four the same and the device
// cannot tell that anything changed. So an import takes over wg-easy's server
// key, its port and its range, and each client's public key, address and
// preshared key.
//
// What it does not take is the clients' private keys, which wg-easy keeps in
// the same file. They are on the devices already; holding a second copy is
// exactly what this package exists not to do.

// WGEasy is the part of wg-easy's wg0.json that matters.
type WGEasy struct {
	Server struct {
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
		Address    string `json:"address"`
	} `json:"server"`
	Clients map[string]struct {
		Name         string `json:"name"`
		Address      string `json:"address"`
		PublicKey    string `json:"publicKey"`
		PreSharedKey string `json:"preSharedKey"`
		Enabled      bool   `json:"enabled"`
	} `json:"clients"`
}

// ImportPlan is what an import would do, shown before it is done.
type ImportPlan struct {
	Subnet    string   `json:"subnet"`
	PublicKey string   `json:"publicKey"`
	Devices   []string `json:"devices"`
	Skipped   []string `json:"skipped,omitempty"`
}

// ParseWGEasy reads wg0.json and checks it is something this can adopt.
func ParseWGEasy(b []byte) (*WGEasy, *ImportPlan, error) {
	var w WGEasy
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, nil, fmt.Errorf("not a wg-easy wg0.json: %w", err)
	}
	raw, err := b64dec(w.Server.PrivateKey)
	if err != nil || len(raw) != 32 {
		return nil, nil, errors.New("the file has no usable server key")
	}
	srv, err := netip.ParseAddr(w.Server.Address)
	if err != nil || !srv.Is4() {
		return nil, nil, errors.New("the file has no usable server address")
	}
	// wg-easy hands out one /24 and puts the server on .1 of it.
	pfx, _ := srv.Prefix(24)
	plan := &ImportPlan{Subnet: pfx.String(), PublicKey: w.Server.PublicKey}
	ids := make([]string, 0, len(w.Clients))
	for id := range w.Clients {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := w.Clients[id]
		switch {
		case !c.Enabled:
			plan.Skipped = append(plan.Skipped, c.Name+" (disabled in wg-easy)")
		case !pfx.Contains(mustAddr(c.Address)):
			plan.Skipped = append(plan.Skipped, c.Name+" (address outside the range)")
		default:
			if _, err := ParseKey(c.PublicKey); err != nil {
				plan.Skipped = append(plan.Skipped, c.Name+" (unusable key)")
				continue
			}
			plan.Devices = append(plan.Devices, c.Name)
		}
	}
	return &w, plan, nil
}

func mustAddr(s string) netip.Addr {
	a, _ := netip.ParseAddr(strings.TrimSpace(s))
	return a
}

// ImportWGEasy adopts a wg-easy server. This server must not have a VPN of its
// own yet, since the key and the range are being replaced. Devices arrive with
// internet access only — the level wg-easy gave them — and anything more is a
// deliberate change on the VPN page afterwards.
func (s *Service) ImportWGEasy(ctx context.Context, actor string, file []byte, endpoint string, port int) (*ImportPlan, error) {
	w, plan, err := ParseWGEasy(file)
	if err != nil {
		return nil, err
	}
	var n int
	_ = s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM vpn_peers WHERE server_id = ?`, s.st.ServerID).Scan(&n)
	if n > 0 {
		return nil, errors.New("this server already has VPN devices; an import replaces the server key, so remove them first")
	}
	if port == 0 {
		port = DefaultPort
	}
	c := Config{Endpoint: endpoint, Port: port, Subnet: plan.Subnet, DNS: DefaultDNS}
	if err := c.validate(); err != nil {
		return nil, err
	}
	raw, _ := b64dec(w.Server.PrivateKey)
	if err := s.saveKey(ctx, raw); err != nil {
		return nil, err
	}
	c.PublicKey = w.Server.PublicKey
	c.Enabled = false // stays off until the rows are in, then Enable applies it all
	if err := s.saveConfig(ctx, c); err != nil {
		return nil, err
	}
	for _, cl := range w.Clients {
		if !cl.Enabled || !contains(plan.Devices, cl.Name) {
			continue
		}
		in := PeerInput{Name: cl.Name, PublicKey: cl.PublicKey, Access: AccessInternet}
		if err := in.validate("device"); err != nil {
			in.Name = "wg-easy " + newID()
		}
		pskEnc := ""
		if psk, err := b64dec(cl.PreSharedKey); err == nil && len(psk) == 32 {
			pskEnc, _ = s.sealRaw(psk)
		} else {
			// wg-easy without a preshared key: a zero key is what WireGuard
			// treats as none, so the device keeps working.
			pskEnc, _ = s.sealRaw(make([]byte, 32))
		}
		if _, err := s.st.DB.ExecContext(ctx, `INSERT INTO vpn_peers
			(id, server_id, name, kind, public_key, psk_enc, address, access, ports, full_tunnel, created_by)
			VALUES (?, ?, ?, 'device', ?, ?, ?, ?, '', 1, ?)`,
			newID(), s.st.ServerID, in.Name, strings.TrimSpace(cl.PublicKey), pskEnc, strings.TrimSpace(cl.Address), AccessInternet, actor); err != nil {
			return nil, uniqueErr(err)
		}
	}
	_ = s.st.Audit(ctx, actor, "vpn.import.wgeasy", plan.Subnet, fmt.Sprintf("%d devices", len(plan.Devices)))
	if _, err := s.Enable(ctx, actor, c); err != nil {
		return nil, err
	}
	return plan, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
