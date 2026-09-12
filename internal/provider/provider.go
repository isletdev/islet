// Package provider talks to the hosting provider's API for the few things a
// panel wants from it: a snapshot before a risky change. Hetzner Cloud is
// the first provider; the token is stored encrypted.
package provider

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/store"
)

// ErrNotConfigured means no provider token is stored.
var ErrNotConfigured = errors.New("no hosting provider configured")

// Service holds the provider configuration.
type Service struct {
	st   *store.Store
	keys *auth.Keys
	HTTP *http.Client
	Base string // Hetzner API base, overridable in tests
	// PublicIP finds this server among the provider's; defaults to proxy.PublicIP.
	PublicIP func(ctx context.Context) string
}

func New(st *store.Store, keys *auth.Keys) *Service {
	return &Service{st: st, keys: keys, HTTP: &http.Client{Timeout: 20 * time.Second}, Base: "https://api.hetzner.cloud/v1", PublicIP: proxy.PublicIP}
}

// State is what the panel shows.
type State struct {
	Kind         string `json:"kind"` // "" | hetzner
	Configured   bool   `json:"configured"`
	LastSnapshot string `json:"lastSnapshot"`
	LastReason   string `json:"lastReason"`
}

func (s *Service) State(ctx context.Context) State {
	kind, _, _ := s.st.Setting(ctx, "provider.kind")
	tok, _ := s.token(ctx)
	last, _, _ := s.st.Setting(ctx, "provider.last_snapshot")
	reason, _, _ := s.st.Setting(ctx, "provider.last_reason")
	return State{Kind: kind, Configured: tok != "", LastSnapshot: last, LastReason: reason}
}

func (s *Service) token(ctx context.Context) (string, error) {
	v, _, err := s.st.Setting(ctx, "provider.token")
	if err != nil || v == "" {
		return "", ErrNotConfigured
	}
	b, err := hex.DecodeString(v)
	if err != nil {
		return "", err
	}
	p, err := s.keys.Decrypt(b)
	if err != nil {
		return "", err
	}
	return string(p), nil
}

// SetToken stores (or clears, with an empty token) the provider token
// after checking it works.
func (s *Service) SetToken(ctx context.Context, actor, kind, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		_ = s.st.SetSetting(ctx, "provider.token", "")
		_ = s.st.SetSetting(ctx, "provider.kind", "")
		_ = s.st.Audit(ctx, actor, "provider.token", "", "cleared")
		return nil
	}
	if kind != "hetzner" {
		return errors.New("only Hetzner Cloud is supported for now")
	}
	if _, err := s.findServer(ctx, token); err != nil {
		return err
	}
	enc, err := s.keys.Encrypt([]byte(token))
	if err != nil {
		return err
	}
	if err := s.st.SetSetting(ctx, "provider.token", hex.EncodeToString(enc)); err != nil {
		return err
	}
	_ = s.st.SetSetting(ctx, "provider.kind", kind)
	_ = s.st.Audit(ctx, actor, "provider.token", kind, "set")
	return nil
}

type hServer struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	PublicNet struct {
		IPv4 struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
	} `json:"public_net"`
}

func (s *Service) api(ctx context.Context, token, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.Base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("the provider rejected the token (needs read and write permission)")
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		_ = json.Unmarshal(out, &e)
		if e.Error.Message == "" {
			e.Error.Message = resp.Status
		}
		return nil, errors.New("provider: " + e.Error.Message)
	}
	return out, nil
}

// findServer matches this machine by public IP, then by host name.
func (s *Service) findServer(ctx context.Context, token string) (*hServer, error) {
	out, err := s.api(ctx, token, http.MethodGet, "/servers?per_page=50", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Servers []hServer `json:"servers"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	ip := ""
	if s.PublicIP != nil {
		ip = s.PublicIP(ctx)
	}
	for i := range list.Servers {
		if ip != "" && list.Servers[i].PublicNet.IPv4.IP == ip {
			return &list.Servers[i], nil
		}
	}
	for i := range list.Servers {
		if strings.EqualFold(list.Servers[i].Name, s.st.Hostname) {
			return &list.Servers[i], nil
		}
	}
	return nil, fmt.Errorf("no server in that project has this machine's address (%s) or name (%s)", ip, s.st.Hostname)
}

// Snapshot asks the provider for a snapshot of this server and returns its
// description. It does not wait for completion.
func (s *Service) Snapshot(ctx context.Context, actor, reason string) (string, error) {
	token, err := s.token(ctx)
	if err != nil {
		return "", err
	}
	srv, err := s.findServer(ctx, token)
	if err != nil {
		return "", err
	}
	desc := "islet-" + strings.ReplaceAll(reason, " ", "-") + "-" + time.Now().UTC().Format("20060102-1504")
	if _, err := s.api(ctx, token, http.MethodPost, fmt.Sprintf("/servers/%d/actions/create_image", srv.ID), map[string]string{"type": "snapshot", "description": desc}); err != nil {
		return "", err
	}
	_ = s.st.SetSetting(ctx, "provider.last_snapshot", time.Now().UTC().Format(time.RFC3339))
	_ = s.st.SetSetting(ctx, "provider.last_reason", reason)
	_ = s.st.Audit(ctx, actor, "provider.snapshot", desc, reason)
	return desc, nil
}
