package vpn

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/notify"
)

// Start brings the host back to what the rows say — an interface does not
// survive a reboot, and nothing else on the machine knows to recreate it —
// and then watches for expiries and connections.
//
// The lockdown is loaded before this returns, so the daemon never answers a
// request with the gate open that should be shut. The rest runs in the
// background: the first apply may install wireguard-tools, and the panel
// should not wait on apt for that.
func (s *Service) Start(ctx context.Context) {
	l := s.loadLockdown(ctx)
	s.lockMu.Lock()
	s.lock = l
	s.lockMu.Unlock()
	if l.Pending {
		// The daemon went down while a lockdown waited for its confirmation.
		// Nobody can have confirmed it, and whoever started it may be locked
		// out right now, so it comes off.
		_ = s.undo(ctx, "system", l)
		_ = s.st.Audit(ctx, "system", "vpn.lockdown.revert", describe(l), "the daemon restarted before it was confirmed")
	}
	go func() {
		if s.Config(ctx).Enabled {
			if err := s.Apply(ctx, "system"); err != nil {
				s.emit(ctx, notify.Critical, "The VPN did not come up", err.Error(), "")
			}
		}
		if u, ok := s.loadUplink(ctx); ok && u.Endpoint != "" {
			if err := s.applyUplink(ctx, "system"); err != nil {
				s.emit(ctx, notify.Critical, "The link to the controller's VPN did not come up", err.Error(), "")
			}
		}
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.tick(ctx)
			}
		}
	}()
}

// tick takes expired peers out, and says when a device connects for the
// first time or from somewhere new.
func (s *Service) tick(ctx context.Context) {
	c := s.Config(ctx)
	if !c.Enabled {
		return
	}
	peers, err := s.Peers(ctx)
	if err != nil {
		return
	}
	var live []Peer
	for _, p := range peers {
		if !p.Expired {
			live = append(live, p)
		}
	}
	s.watchMu.Lock()
	changed := s.active != activeKey(live)
	s.watchMu.Unlock()
	if changed {
		for _, p := range peers {
			if p.Expired {
				_ = s.st.Audit(ctx, "system", "vpn.peer.expired", p.Name, p.ExpiresAt)
			}
		}
		if err := s.Apply(ctx, "system"); err != nil {
			s.emit(ctx, notify.Warning, "Could not remove an expired VPN device", err.Error(), "")
		}
	}
	state, err := s.live(ctx, Iface)
	if err != nil {
		return
	}
	now := s.now()
	for _, p := range live {
		l := state[p.PublicKey]
		up := l != nil && !l.Handshake.IsZero() && now.Sub(l.Handshake) < online
		s.watchMu.Lock()
		was := s.online[p.ID]
		s.online[p.ID] = up
		s.watchMu.Unlock()
		if !up || was {
			continue
		}
		s.connected(ctx, p, l)
	}
}

// connected runs when a peer comes online. A first connection is always
// worth a line; after that, only a new country is, because a phone changes
// address every time it changes network and an alert per address is noise
// that teaches people to ignore the channel.
func (s *Service) connected(ctx context.Context, p Peer, l *Live) {
	ip := ""
	if l.Endpoint != "" {
		ip, _, _ = net.SplitHostPort(l.Endpoint)
	}
	if p.FirstSeenAt == "" {
		_, _ = s.st.DB.ExecContext(ctx, `UPDATE vpn_peers SET first_seen_at = ? WHERE server_id = ? AND id = ?`,
			s.now().UTC().Format(time.RFC3339), s.st.ServerID, p.ID)
		s.emit(ctx, notify.Info, "New device on the VPN: "+p.Name, fmt.Sprintf("%s connected for the first time, from %s.", p.Name, orUnknown(ip)), p.Name)
	}
	if s.hooks.Country == nil || ip == "" {
		return
	}
	country := s.hooks.Country(ctx, ip)
	if country == "" {
		return
	}
	var seen string
	_ = s.st.DB.QueryRowContext(ctx, `SELECT countries FROM vpn_peers WHERE server_id = ? AND id = ?`, s.st.ServerID, p.ID).Scan(&seen)
	for _, c := range strings.Split(seen, "|") {
		if c == country {
			return
		}
	}
	list := country
	if seen != "" {
		list = seen + "|" + country
	}
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE vpn_peers SET countries = ? WHERE server_id = ? AND id = ?`, list, s.st.ServerID, p.ID)
	if seen != "" {
		s.emit(ctx, notify.Warning, "VPN device in a new country: "+p.Name,
			fmt.Sprintf("%s connected from %s (%s), which it has not done before. If that is not you, remove the device from the VPN page.", p.Name, country, ip), p.Name)
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown address"
	}
	return s
}

func (s *Service) emit(ctx context.Context, sev, title, msg, subject string) {
	if s.bus == nil {
		return
	}
	s.bus.Emit(ctx, notify.Event{Category: "security", Severity: sev, Title: title, Message: msg, Link: "/vpn", Subject: subject})
}

func b64dec(v string) ([]byte, error) { return base64.StdEncoding.DecodeString(v) }
