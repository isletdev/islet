package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/isletdev/islet/internal/fleet"
	"github.com/isletdev/islet/internal/version"
	"github.com/isletdev/islet/pkg/api"
)

// Managing more than one server.
//
// Every route here is admin-only. Adding a server means handing this panel root
// on another machine, and reaching one means acting as an administrator there.

func (s *Server) fleetErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fleet.ErrNotFound):
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "this panel does not manage that server"})
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "fleet", Message: err.Error()})
	}
}

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	list, err := s.fleet.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	// The panel's own machine is always first and is not a row in the table: it
	// is where this is running, and it cannot be removed or joined.
	writeJSON(w, http.StatusOK, map[string]any{
		"local": map[string]any{
			"id": "local", "name": "This server", "hostname": s.store.Hostname,
			"status": "ready", "version": version.Version,
		},
		"servers": list,
	})
}

// handleServerKey returns the public key a person can install themselves when
// they would rather not type a password into a browser at all.
func (s *Server) handleServerKey(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	key, err := s.fleet.PublicKey(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": key})
}

func (s *Server) handleServerAdd(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Name      string `json:"name"`
		Host      string `json:"host"`
		SSHPort   int    `json:"sshPort"`
		SSHUser   string `json:"sshUser"`
		PanelPort int    `json:"panelPort"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	v, err := s.fleet.Add(r.Context(), userFrom(r.Context()).Username, fleet.Server{
		Name: req.Name, Host: req.Host, SSHPort: req.SSHPort, SSHUser: req.SSHUser, PanelPort: req.PanelPort,
	})
	if err != nil {
		s.fleetErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// handleServerJoin starts the install. The credentials it takes are used once
// and never stored; what persists is this panel's own key.
func (s *Server) handleServerJoin(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		User       string `json:"user"`
		Password   string `json:"password"`
		PrivateKey string `json:"privateKey"`
		Passphrase string `json:"passphrase"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	err := s.fleet.StartJoin(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"),
		fleet.Credentials{User: req.User, Password: req.Password, PrivateKey: req.PrivateKey, Passphrase: req.Passphrase},
		version.Version)
	if err != nil {
		s.fleetErr(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleServerJoinEvents streams the install so the wizard can show it
// happening. Reconnecting replays what has already been printed.
func (s *Server) handleServerJoinEvents(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: "streaming unsupported"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	// no-transform is the part that is not about caching: it tells a CDN or a
	// proxy not to recompress this, and a compressor in front of a stream holds
	// its first kilobyte back — which for a stream is however long the work
	// takes. Traefik's own compressor is told the same thing by content type,
	// in the middleware Islet writes for it.
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s.fleet.Follow(r.Context(), r.PathValue("id"), func(p fleet.Progress) {
		b, err := json.Marshal(p)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: progress\ndata: %s\n\n", b)
		fl.Flush()
	})
	fmt.Fprintf(w, "event: end\ndata: %q\n\n", "done")
	fl.Flush()
}

func (s *Server) handleServerForget(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := s.fleet.Forget(r.Context(), userFrom(r.Context()).Username, r.PathValue("id")); err != nil {
		s.fleetErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleServerCheck(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := s.fleet.Reachable(r.Context(), id); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	v, err := s.fleet.Get(r.Context(), id)
	if err != nil {
		s.fleetErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "server": v})
}

// handleServerExposure answers one question the person cannot check from a
// browser: is that server's panel port open to the internet?
func (s *Server) handleServerExposure(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	open, err := s.fleet.PanelExposed(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fleetErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"panelOpen": open})
}

// handleServerProxy forwards a request to a managed server's own panel.
//
// This is what makes every existing page work against another machine without
// the page knowing: /api/v1/servers/{id}/proxy/domains reaches that server's
// /api/v1/domains. The session stays here and is checked here; the far end sees
// this panel's token and nothing about the person.
func (s *Server) handleServerProxy(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	id := r.PathValue("id")
	rest := strings.TrimPrefix(r.PathValue("rest"), "/")
	if rest == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "nothing to forward"})
		return
	}
	// Never let a forwarded path climb out of the API.
	if strings.Contains(rest, "..") {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "bad path"})
		return
	}

	// A WebSocket cannot travel on an http.Client, and the terminal is a
	// WebSocket. It goes down the same tunnel, spliced rather than parsed.
	if isUpgrade(r) {
		s.proxyUpgrade(w, r, id, rest)
		return
	}

	cl, token, err := s.fleet.Client(r.Context(), id)
	if err != nil {
		s.fleetErr(w, err)
		return
	}
	url := "https://islet/api/v1/" + rest
	if q := r.URL.RawQuery; q != "" {
		url += "?" + q
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	for _, k := range []string{"Content-Type", "Accept", "Range"} {
		if v := r.Header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// The far end applies the same origin checks this one does, and this request
	// did not come from a browser there.
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := cl.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "unreachable", Message: "could not reach that server: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		switch k {
		case "Content-Type", "Content-Length", "Content-Disposition", "Cache-Control", "X-Accel-Buffering":
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
	}
	w.WriteHeader(resp.StatusCode)
	// Flush as it arrives so a forwarded stream stays a stream.
	if fl, ok := w.(http.Flusher); ok {
		buf := make([]byte, 8<<10)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				fl.Flush()
			}
			if err != nil {
				return
			}
		}
	}
	_, _ = io.Copy(w, resp.Body)
}

func isUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// proxyUpgrade carries a WebSocket to a managed server.
//
// The handshake is rebuilt rather than forwarded: the far end must see a
// request that looks local to it, with this panel's token and an origin that
// matches the host it thinks it is. After the 101 the two connections are just
// bytes, and nothing here understands frames.
func (s *Server) proxyUpgrade(w http.ResponseWriter, r *http.Request, id, rest string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: "this connection cannot be upgraded"})
		return
	}
	back, token, err := s.fleet.Dial(r.Context(), id)
	if err != nil {
		s.fleetErr(w, err)
		return
	}
	defer back.Close()

	path := "/api/v1/" + rest
	if q := r.URL.RawQuery; q != "" {
		path += "?" + q
	}
	var head bytes.Buffer
	fmt.Fprintf(&head, "GET %s HTTP/1.1\r\nHost: islet\r\n", path)
	for _, k := range []string{"Upgrade", "Connection", "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Protocol", "Sec-Websocket-Extensions"} {
		if v := r.Header.Get(k); v != "" {
			fmt.Fprintf(&head, "%s: %s\r\n", k, v)
		}
	}
	fmt.Fprintf(&head, "Authorization: Bearer %s\r\n", token)
	fmt.Fprint(&head, "Origin: https://islet\r\nSec-Fetch-Site: same-origin\r\n\r\n")
	if _, err := back.Write(head.Bytes()); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "unreachable", Message: err.Error()})
		return
	}

	br := bufio.NewReader(back)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "unreachable", Message: "that server refused the connection: " + err.Error()})
		return
	}
	// A refusal is still an ordinary response, and saying so is more use than a
	// socket that closes without explanation.
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	front, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer front.Close()
	if err := resp.Write(buf); err != nil {
		return
	}
	if err := buf.Flush(); err != nil {
		return
	}
	splice(front, back, br)
}

// splice copies until either side stops. The reader carries whatever arrived in
// the same read as the handshake response, which would otherwise be lost.
func splice(front net.Conn, back net.Conn, buffered *bufio.Reader) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(back, front); _ = back.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(front, buffered); _ = front.Close(); done <- struct{}{} }()
	<-done
}
