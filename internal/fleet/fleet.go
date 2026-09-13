// Package fleet lets one panel manage more than one server.
//
// The shape, and why:
//
// Every managed server runs the same daemon with its own database. It is a
// complete Islet, not an agent. Its cron jobs, backups, uptime checks and
// deploys keep running when this panel is switched off, and if this panel is
// lost the server can be opened on its own. There is no second mode to write or
// to test, because there is no second mode.
//
// The controller reaches a managed server over SSH, through the same connection
// that installed it. That means a managed server needs no port open to the
// internet at all: its panel listens on loopback and this tunnels to it. It also
// means there is exactly one credential to look after, the controller's own key,
// and revoking it is deleting one line from authorized_keys.
//
// Sessions stay here. The controller holds an API token for each server,
// encrypted, and forwards requests with it, so a person signs in once.
package fleet

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/store"
)

// Server is a machine this panel manages.
type Server struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	SSHPort    int    `json:"sshPort"`
	SSHUser    string `json:"sshUser"`
	PanelPort  int    `json:"panelPort"`
	Status     string `json:"status"`
	StatusNote string `json:"statusNote,omitempty"`
	Version    string `json:"version,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	LastSeen   string `json:"lastSeen,omitempty"`
	CreatedAt  string `json:"createdAt"`
	CreatedBy  string `json:"createdBy,omitempty"`
}

// Service owns the address book and the connections to it.
type Service struct {
	st   *store.Store
	keys *auth.Keys

	mu    sync.Mutex
	conns map[string]*pooled

	jobMu sync.Mutex
	jobs  map[string]*job
}

type pooled struct {
	conn *Conn
	used time.Time
}

func New(st *store.Store, keys *auth.Keys) *Service {
	return &Service{st: st, keys: keys, conns: map[string]*pooled{}, jobs: map[string]*job{}}
}

// ---- the address book ----

func (s *Service) List(ctx context.Context) ([]Server, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, name, host, ssh_port, ssh_user, panel_port,
		status, status_note, version, hostname, last_seen, created_at, created_by
		FROM fleet_servers WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		var v Server
		if err := rows.Scan(&v.ID, &v.Name, &v.Host, &v.SSHPort, &v.SSHUser, &v.PanelPort,
			&v.Status, &v.StatusNote, &v.Version, &v.Hostname, &v.LastSeen, &v.CreatedAt, &v.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (*Server, error) {
	var v Server
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, name, host, ssh_port, ssh_user, panel_port,
		status, status_note, version, hostname, last_seen, created_at, created_by
		FROM fleet_servers WHERE server_id = ? AND id = ?`, s.st.ServerID, id).
		Scan(&v.ID, &v.Name, &v.Host, &v.SSHPort, &v.SSHUser, &v.PanelPort,
			&v.Status, &v.StatusNote, &v.Version, &v.Hostname, &v.LastSeen, &v.CreatedAt, &v.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &v, err
}

// ErrNotFound is returned for an id this panel does not manage.
var ErrNotFound = errors.New("no such server")

// seal and open wrap the byte-oriented key store, and encode so the result can
// live in a text column.
func (s *Service) seal(v string) (string, error) {
	b, err := s.keys.Encrypt([]byte(v))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) open(v string) (string, error) {
	raw, err := hex.DecodeString(v)
	if err != nil {
		return "", err
	}
	b, err := s.keys.Decrypt(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// newID is a short random identifier, the same shape the rest of the daemon uses.
func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Add records a server before the join starts, so the wizard has something to
// attach progress to and a failed join leaves a row explaining why.
func (s *Service) Add(ctx context.Context, actor string, v Server) (*Server, error) {
	v.Name = strings.TrimSpace(v.Name)
	v.Host = strings.TrimSpace(v.Host)
	if v.Name == "" || len(v.Name) > 60 {
		return nil, errors.New("give the server a name, up to 60 characters")
	}
	if v.Host == "" {
		return nil, errors.New("give the server's address")
	}
	if strings.ContainsAny(v.Host, " /\\") {
		return nil, errors.New("that does not look like a host name or address")
	}
	if v.SSHPort <= 0 || v.SSHPort > 65535 {
		v.SSHPort = 22
	}
	if v.PanelPort <= 0 || v.PanelPort > 65535 {
		v.PanelPort = 9443
	}
	if strings.TrimSpace(v.SSHUser) == "" {
		v.SSHUser = "root"
	}
	v.ID = newID()
	v.Status = "pending"
	_, err := s.st.DB.ExecContext(ctx, `INSERT INTO fleet_servers
		(id, server_id, name, host, ssh_port, ssh_user, panel_port, status, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
		v.ID, s.st.ServerID, v.Name, v.Host, v.SSHPort, v.SSHUser, v.PanelPort, actor)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("a server with that name is already here")
		}
		return nil, err
	}
	_ = s.st.Audit(ctx, actor, "fleet.add", v.Name, v.Host)
	return s.Get(ctx, v.ID)
}

// Forget removes a server from this panel.
//
// It deliberately changes nothing on the server itself: it keeps running, keeps
// serving, and can be opened on its own or adopted again. Removing the
// controller's access is deleting its key there, which the panel explains.
func (s *Service) Forget(ctx context.Context, actor, id string) error {
	v, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	s.drop(id)
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM fleet_servers WHERE server_id = ? AND id = ?`, s.st.ServerID, id); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "fleet.forget", v.Name, v.Host)
	return nil
}

func (s *Service) setStatus(ctx context.Context, id, status, note string) {
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE fleet_servers SET status = ?, status_note = ? WHERE server_id = ? AND id = ?`,
		status, note, s.st.ServerID, id)
}

// saveJoin records what a successful join produced.
func (s *Service) saveJoin(ctx context.Context, id string, r *Result) error {
	tok, err := s.seal(r.Token)
	if err != nil {
		return err
	}
	_, err = s.st.DB.ExecContext(ctx, `UPDATE fleet_servers
		SET api_token_enc = ?, host_key = ?, version = ?, hostname = ?, status = 'ready', status_note = '',
		    last_seen = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE server_id = ? AND id = ?`, tok, r.HostKey, r.Version, r.Hostname, s.st.ServerID, id)
	return err
}

func (s *Service) credentials(ctx context.Context, id string) (token, hostKey string, err error) {
	var enc string
	err = s.st.DB.QueryRowContext(ctx, `SELECT api_token_enc, host_key FROM fleet_servers WHERE server_id = ? AND id = ?`,
		s.st.ServerID, id).Scan(&enc, &hostKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if enc == "" {
		return "", "", errors.New("this server has not finished joining yet")
	}
	token, err = s.open(enc)
	return token, hostKey, err
}

// ---- reaching a server ----

// key returns the controller's own private key, generating it the first time.
// One key for the whole fleet: it is this panel's identity, and rotating it is a
// single action rather than one per server.
func (s *Service) key(ctx context.Context) (*Keypair, error) {
	enc, _, _ := s.st.Setting(ctx, "fleet.key")
	pubLine, _, _ := s.st.Setting(ctx, "fleet.key.pub")
	if enc != "" && pubLine != "" {
		priv, err := s.open(enc)
		if err != nil {
			return nil, err
		}
		return &Keypair{PrivatePEM: priv, PublicLine: pubLine}, nil
	}
	kp, err := NewKeypair("islet-panel")
	if err != nil {
		return nil, err
	}
	sealed, err := s.seal(kp.PrivatePEM)
	if err != nil {
		return nil, err
	}
	if err := s.st.SetSetting(ctx, "fleet.key", sealed); err != nil {
		return nil, err
	}
	if err := s.st.SetSetting(ctx, "fleet.key.pub", kp.PublicLine); err != nil {
		return nil, err
	}
	return kp, nil
}

// PublicKey is what a person adds by hand when they would rather not hand over
// a password at all.
func (s *Service) PublicKey(ctx context.Context) (string, error) {
	kp, err := s.key(ctx)
	if err != nil {
		return "", err
	}
	return kp.PublicLine, nil
}

// connect returns a live SSH connection, reusing one when it is still good.
func (s *Service) connect(ctx context.Context, v *Server) (*Conn, error) {
	s.mu.Lock()
	if p, ok := s.conns[v.ID]; ok {
		p.used = time.Now()
		c := p.conn
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()

	kp, err := s.key(ctx)
	if err != nil {
		return nil, err
	}
	_, hostKey, err := s.credentials(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	conn, err := Dial(ctx, fmt.Sprintf("%s:%d", v.Host, v.SSHPort),
		Credentials{User: v.SSHUser, PrivateKey: kp.PrivatePEM}, hostKey)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.conns[v.ID] = &pooled{conn: conn, used: time.Now()}
	s.mu.Unlock()
	return conn, nil
}

func (s *Service) drop(id string) {
	s.mu.Lock()
	if p, ok := s.conns[id]; ok {
		_ = p.conn.Close()
		delete(s.conns, id)
	}
	s.mu.Unlock()
}

// Client returns an HTTP client whose requests reach the managed server's panel
// through the SSH connection, with the controller's token attached.
func (s *Service) Client(ctx context.Context, id string) (*http.Client, string, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	token, _, err := s.credentials(ctx, id)
	if err != nil {
		return nil, "", err
	}
	conn, err := s.connect(ctx, v)
	if err != nil {
		s.drop(id)
		return nil, "", err
	}
	target := fmt.Sprintf("127.0.0.1:%d", v.PanelPort)
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return conn.Tunnel(ctx, target)
		},
		// The far end serves its own self-signed certificate on loopback. The
		// connection is already inside an authenticated SSH channel to a pinned
		// host key, which is what is actually protecting it here.
		TLSClientConfig:     insecureLoopbackTLS(),
		DisableCompression:  true,
		MaxIdleConnsPerHost: 2,
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Minute}, token, nil
}

// Reachable checks a server and records what it found.
func (s *Service) Reachable(ctx context.Context, id string) error {
	cl, token, err := s.Client(ctx, id)
	if err != nil {
		s.setStatus(ctx, id, "unreachable", err.Error())
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://islet/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := cl.Do(req)
	if err != nil {
		s.drop(id)
		s.setStatus(ctx, id, "unreachable", err.Error())
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.setStatus(ctx, id, "unreachable", "the panel answered "+resp.Status)
		return fmt.Errorf("health: %s", resp.Status)
	}
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE fleet_servers SET status = 'ready', status_note = '',
		last_seen = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE server_id = ? AND id = ?`, s.st.ServerID, id)
	return nil
}

// insecureLoopbackTLS accepts the managed panel's self-signed certificate.
//
// This is not a hole. The request never touches a network: it travels inside an
// SSH channel to a pinned host key and comes out on that server's loopback
// interface. The certificate adds nothing there, and demanding a valid one
// would mean provisioning a certificate authority for a connection that is
// already authenticated.
func insecureLoopbackTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
}

// Dial opens a raw connection to a managed server's panel, already through the
// tunnel and already inside TLS.
//
// The HTTP client above cannot carry a WebSocket, and the terminal is a
// WebSocket. Rather than inventing a second way to reach a server, this is the
// same path with the handshake written by hand and the bytes copied both ways.
func (s *Service) Dial(ctx context.Context, id string) (net.Conn, string, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	token, _, err := s.credentials(ctx, id)
	if err != nil {
		return nil, "", err
	}
	conn, err := s.connect(ctx, v)
	if err != nil {
		s.drop(id)
		return nil, "", err
	}
	raw, err := conn.Tunnel(ctx, fmt.Sprintf("127.0.0.1:%d", v.PanelPort))
	if err != nil {
		s.drop(id)
		return nil, "", err
	}
	tc := tls.Client(raw, insecureLoopbackTLS())
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, "", err
	}
	return tc, token, nil
}

// PanelExposed reports whether a managed server's panel port answers over the
// open internet.
//
// It does not need to. This panel reaches a managed server through SSH, so the
// port can be closed entirely. The join deliberately does not close it by
// itself: someone may be using that address, and a tool that silently locks a
// person out of their own server is worse than one that says what it found.
func (s *Service) PanelExposed(ctx context.Context, id string) (bool, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return false, err
	}
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(v.Host, strconv.Itoa(v.PanelPort)))
	if err != nil {
		return false, nil
	}
	_ = c.Close()
	return true, nil
}
