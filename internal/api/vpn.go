package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/vpn"
	"github.com/isletdev/islet/pkg/api"
)

// vpnGate is the daemon's half of "the panel only over the VPN": with the
// lockdown on, nobody signs in or acts signed in except from the tunnel, this
// machine, or the fallback address. The firewall closes the panel port; this
// closes the domain the panel may also be published on, which arrives through
// the proxy where no firewall rule on the panel port can see it.
func (s *Server) vpnGate(w http.ResponseWriter, r *http.Request) bool {
	if s.vpn == nil || s.vpn.Allowed(clientIP(r)) {
		return true
	}
	c := s.vpn.Config(r.Context())
	writeJSON(w, http.StatusForbidden, api.Error{Error: "vpn_only",
		Message: fmt.Sprintf("This panel is reachable only over its VPN. Connect, then open https://%s:%d.", c.Address(), s.vpn.Lockdown().PanelPort)})
	return false
}

var publicIP struct {
	sync.Mutex
	v  string
	at time.Time
}

// suggestEndpoint is what the form offers as the address devices dial. The
// panel's own host name is the wrong default whenever the panel sits behind a
// CDN, which proxies HTTP and drops UDP, so it is the server's public address.
func suggestEndpoint(ctx context.Context) string {
	publicIP.Lock()
	defer publicIP.Unlock()
	if publicIP.v == "" || time.Since(publicIP.at) > time.Hour {
		publicIP.v, publicIP.at = proxy.PublicIP(ctx), time.Now()
	}
	return publicIP.v
}

func (s *Server) handleVPN(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st, err := s.vpn.Status(r.Context())
		if err != nil {
			s.failed(w, "vpn", err)
			return
		}
		ip := clientIP(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            st,
			"suggestedEndpoint": suggestEndpoint(r.Context()),
			"clientIp":          ip,
			"inTunnel":          s.vpn.InTunnel(r.Context(), ip),
			"panelPort":         s.vpn.PanelPort(),
			"reach":             s.vpnReach(r),
		})
	case http.MethodPost:
		if !s.adminOnly(w, r) {
			return
		}
		var c vpn.Config
		if err := decode(r, &c); err != nil {
			s.badJSON(w, err)
			return
		}
		out, err := s.vpn.Enable(r.Context(), userFrom(r.Context()).Username, c)
		invalidateScore()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodDelete:
		if !s.adminOnly(w, r) {
			return
		}
		err := s.vpn.Disable(r.Context(), userFrom(r.Context()).Username)
		invalidateScore()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleVPNPeers(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var in vpn.PeerInput
	if err := decode(r, &in); err != nil {
		s.badJSON(w, err)
		return
	}
	iss, err := s.vpn.AddPeer(r.Context(), userFrom(r.Context()).Username, in, "device", "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, iss)
}

func (s *Server) handleVPNPeer(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	actor := userFrom(r.Context()).Username
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodPut:
		var in vpn.PeerInput
		if err := decode(r, &in); err != nil {
			s.badJSON(w, err)
			return
		}
		p, err := s.vpn.UpdatePeer(r.Context(), actor, id, in)
		if err != nil {
			s.vpnErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	case http.MethodDelete:
		if err := s.vpn.RemovePeer(r.Context(), actor, id); err != nil {
			s.vpnErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleVPNRekey(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var in struct {
		PublicKey string `json:"publicKey"`
	}
	if err := decode(r, &in); err != nil {
		s.badJSON(w, err)
		return
	}
	iss, err := s.vpn.Rekey(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"), in.PublicKey)
	if err != nil {
		s.vpnErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, iss)
}

func (s *Server) vpnErr(w http.ResponseWriter, err error) {
	if errors.Is(err, vpn.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
}

func (s *Server) handleVPNLockdown(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var in vpn.LockdownInput
	if err := decode(r, &in); err != nil {
		s.badJSON(w, err)
		return
	}
	if in.SSH && s.security != nil {
		if cfg, _, _ := s.security.SSH(r.Context()); cfg.Port > 0 {
			in.SSHPort = cfg.Port
		}
	}
	err := s.vpn.SetLockdown(r.Context(), userFrom(r.Context()).Username, in, clientIP(r))
	invalidateScore()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.vpn.Lockdown())
}

func (s *Server) handleVPNConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	err := s.vpn.Confirm(r.Context(), userFrom(r.Context()).Username, clientIP(r))
	invalidateScore()
	if err != nil {
		writeJSON(w, http.StatusConflict, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.vpn.Lockdown())
}

// ---- moving from wg-easy ----

type wgEasyFound struct {
	Container string          `json:"container"`
	Name      string          `json:"name"`
	Running   bool            `json:"running"`
	Plan      *vpn.ImportPlan `json:"plan,omitempty"`
	Error     string          `json:"error,omitempty"`
	file      []byte
}

// findWGEasy looks for wg-easy containers and reads each one's wg0.json from
// the volume it keeps it in.
func (s *Server) findWGEasy(ctx context.Context) []wgEasyFound {
	out := []wgEasyFound{}
	res, err := s.runner.Read(ctx, "docker", "ps", "-a", "--format", "{{.ID}} {{.Image}} {{.Names}} {{.State}}")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.Contains(f[1], "wg-easy") {
			continue
		}
		c := wgEasyFound{Container: f[0], Name: f[2], Running: f[3] == "running"}
		src, err := s.runner.Read(ctx, "docker", "inspect", "-f", `{{range .Mounts}}{{if eq .Destination "/etc/wireguard"}}{{.Source}}{{end}}{{end}}`, f[0])
		dir := strings.TrimSpace(src.Stdout)
		if err != nil || dir == "" {
			c.Error = "no /etc/wireguard volume on this container"
			out = append(out, c)
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "wg0.json"))
		if err != nil {
			c.Error = "could not read wg0.json: " + err.Error()
			out = append(out, c)
			continue
		}
		if _, plan, err := vpn.ParseWGEasy(b); err != nil {
			c.Error = err.Error()
		} else {
			c.Plan, c.file = plan, b
		}
		out = append(out, c)
	}
	return out
}

func (s *Server) handleVPNImportScan(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.findWGEasy(r.Context()))
}

// handleVPNImport stops wg-easy, which holds the UDP port, and adopts its
// server and devices. If the adoption fails wg-easy is started again, so a
// failed import costs a minute of VPN rather than the VPN.
func (s *Server) handleVPNImport(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var in struct {
		Container string `json:"container"`
		Endpoint  string `json:"endpoint"`
		Port      int    `json:"port"`
	}
	if err := decode(r, &in); err != nil {
		s.badJSON(w, err)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	actor := userFrom(r.Context()).Username
	var found *wgEasyFound
	for _, c := range s.findWGEasy(ctx) {
		if c.Container == in.Container {
			c := c
			found = &c
		}
	}
	if found == nil || found.Plan == nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: "that wg-easy container was not found, or its configuration could not be read"})
		return
	}
	if found.Running {
		if _, err := s.runner.Run(ctx, actor, "docker", "stop", found.Container); err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: "could not stop wg-easy: " + err.Error()})
			return
		}
	}
	plan, err := s.vpn.ImportWGEasy(ctx, actor, found.file, in.Endpoint, in.Port)
	if err != nil {
		if found.Running {
			_, _ = s.runner.Run(ctx, actor, "docker", "start", found.Container)
		}
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// ---- the member's half of fleet-over-VPN ----

func (s *Server) handleVPNUplinkKey(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	k, err := s.vpn.UplinkKey(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": k})
}

func (s *Server) handleVPNUplink(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	actor := userFrom(r.Context()).Username
	if r.Method == http.MethodDelete {
		if err := s.vpn.LeaveUplink(r.Context(), actor); err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in vpn.UplinkIn
	if err := decode(r, &in); err != nil {
		s.badJSON(w, err)
		return
	}
	if err := s.vpn.JoinUplink(r.Context(), actor, in); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- the controller's half ----

// memberCall makes one request to a fleet server's API through the fleet
// connection.
func (s *Server) memberCall(ctx context.Context, id, method, path string, body, out any) error {
	cl, token, err := s.fleet.Client(ctx, id)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, "https://islet"+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return errors.New("that server's Islet is too old to join a VPN; update it first")
	}
	if resp.StatusCode >= 300 {
		var e api.Error
		if json.Unmarshal(b, &e) == nil && e.Message != "" {
			return errors.New(e.Message)
		}
		return fmt.Errorf("that server answered %s", resp.Status)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// handleVPNFleet puts a fleet server on this server's VPN, or takes it off.
//
// The member makes its key and keeps the private half; this side adds it as a
// peer that can be reached and never reaches back; the member is told where
// to connect; and once a handshake proves the tunnel, the fleet connection
// moves onto it.
func (s *Server) handleVPNFleet(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	actor := userFrom(r.Context()).Username
	id := r.PathValue("id")
	srv, err := s.fleet.Get(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	if r.Method == http.MethodDelete {
		// Back to the public address first, while the tunnel still works to
		// tell the member to leave.
		_ = s.fleet.SetVPNAddress(ctx, actor, id, "")
		leaveErr := s.memberCall(ctx, id, http.MethodDelete, "/api/v1/vpn/uplink", nil, nil)
		if p, err := s.vpn.PeerByFleet(ctx, id); err == nil {
			_ = s.vpn.RemovePeer(ctx, actor, p.ID)
		}
		if leaveErr != nil {
			writeJSON(w, http.StatusOK, map[string]string{"note": "Removed here. The server itself could not be told: " + leaveErr.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c := s.vpn.Config(ctx)
	if !c.Enabled {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: "turn this server's VPN on first"})
		return
	}
	if _, err := s.vpn.PeerByFleet(ctx, id); err == nil {
		writeJSON(w, http.StatusConflict, api.Error{Error: "vpn", Message: srv.Name + " is already on the VPN"})
		return
	}
	var key struct {
		PublicKey string `json:"publicKey"`
	}
	if err := s.memberCall(ctx, id, http.MethodPost, "/api/v1/vpn/uplink/key", nil, &key); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	iss, err := s.vpn.AddPeer(ctx, actor, vpn.PeerInput{Name: "server " + srv.Name, PublicKey: key.PublicKey}, "server", id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	in := vpn.UplinkIn{Endpoint: iss.Endpoint, ServerKey: iss.ServerKey, PresharedKey: iss.PresharedKey, Address: iss.Peer.Address, ServerAddress: c.Address()}
	if err := s.memberCall(ctx, id, http.MethodPost, "/api/v1/vpn/uplink", in, nil); err != nil {
		_ = s.vpn.RemovePeer(ctx, actor, iss.Peer.ID)
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "vpn", Message: err.Error()})
		return
	}
	// The member keeps the tunnel alive every 25 seconds, so the first
	// handshake comes almost at once when the path is open.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !s.vpn.Handshake(ctx, key.PublicKey).IsZero() {
			if err := s.fleet.SetVPNAddress(ctx, actor, id, iss.Peer.Address); err != nil {
				s.failed(w, "vpn", err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"address": iss.Peer.Address})
			return
		}
		time.Sleep(time.Second)
	}
	// No handshake: undo both ends rather than leave a peer that is set up
	// and silent, which is the state nobody can tell apart from working.
	_ = s.memberCall(ctx, id, http.MethodDelete, "/api/v1/vpn/uplink", nil, nil)
	_ = s.vpn.RemovePeer(ctx, actor, iss.Peer.ID)
	writeJSON(w, http.StatusBadGateway, api.Error{Error: "vpn", Message: fmt.Sprintf("%s set up its end but never connected, so nothing was changed. Check that UDP %d reaches this server from it, at %s.", srv.Name, c.Port, iss.Endpoint)})
}

// vpnReach is what a device in the tunnel can open on this server, with the
// container behind each port Docker publishes. Only while the VPN is on, and
// only for an admin: it is a list of every listening socket.
func (s *Server) vpnReach(r *http.Request) []vpn.Reachable {
	if !s.vpn.Config(r.Context()).Enabled || userFrom(r.Context()).Role != "admin" {
		return nil
	}
	out := s.vpn.Reach(r.Context())
	if s.docker == nil {
		return out
	}
	cs, err := s.docker.Containers(r.Context(), "")
	if err != nil {
		return out
	}
	app := map[int]string{}
	for _, c := range cs {
		for _, p := range vpn.PublishedPorts(c.Ports) {
			app[p] = c.Name
		}
	}
	for i := range out {
		out[i].App = app[out[i].Port]
	}
	return out
}
