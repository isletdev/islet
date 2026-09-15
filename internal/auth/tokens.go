package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Token is an API token as shown in the list (never the secret).
type Token struct {
	ID         string `json:"id"`
	UserID     string `json:"userId"`
	Name       string `json:"name"`
	Scopes     string `json:"scopes"`
	LastUsedAt string `json:"lastUsedAt"`
	ExpiresAt  string `json:"expiresAt"`
	CreatedAt  string `json:"createdAt"`
	Prefix     string `json:"prefix"`
}

// Scopes a token may carry, in the order the panel offers them. "*" grants
// everything the user can do and is chosen by picking none of these.
//
// "shell" is root on the server — it opens the host terminal and container
// exec — so it is deliberately separate from everything else, and "security"
// can take the firewall down, so it is its own grant too. Nothing here lets a
// token mint another: see ScopeAllows.
var Scopes = []string{
	"read", "deploy", "cron", "db", "containers", "domains", "files",
	"backups", "security", "uptime", "runners", "catalog", "workspaces",
	"notify", "logs", "system", "settings", "shell",
}

// ErrBadToken is returned for unknown, expired or malformed tokens.
var ErrBadToken = errors.New("invalid api token")

const tokenPrefix = "islet_"

// CreateToken mints a token for a user and returns the one-time secret.
func (s *Service) CreateToken(ctx context.Context, userID, name, scopes string, ttl time.Duration) (string, *Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return "", nil, errors.New("name must be 1-60 characters")
	}
	scopes = strings.TrimSpace(scopes)
	if scopes == "" {
		scopes = "*"
	}
	if scopes != "*" {
		for _, sc := range strings.Split(scopes, ",") {
			ok := false
			for _, known := range Scopes {
				if known == strings.TrimSpace(sc) {
					ok = true
				}
			}
			if !ok {
				return "", nil, errors.New("unknown scope " + sc)
			}
		}
	}
	secret, err := randomHex(24)
	if err != nil {
		return "", nil, err
	}
	plain := tokenPrefix + secret
	id, err := randomHex(6)
	if err != nil {
		return "", nil, err
	}
	exp := ""
	if ttl > 0 {
		exp = s.now().Add(ttl).UTC().Format(time.RFC3339)
	}
	if _, err := s.st.DB.ExecContext(ctx, `INSERT INTO api_tokens (id, server_id, user_id, name, token_hash, scopes, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, s.st.ServerID, userID, name, hashToken(plain), scopes, exp); err != nil {
		return "", nil, err
	}
	return plain, &Token{ID: id, UserID: userID, Name: name, Scopes: scopes, ExpiresAt: exp, Prefix: plain[:12]}, nil
}

// UserByToken resolves a bearer token to its user and scopes.
func (s *Service) UserByToken(ctx context.Context, plain string) (*User, *Token, error) {
	if !strings.HasPrefix(plain, tokenPrefix) || len(plain) < 20 {
		return nil, nil, ErrBadToken
	}
	var t Token
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, user_id, name, scopes, last_used_at, expires_at, created_at FROM api_tokens WHERE token_hash = ?`, hashToken(plain)).
		Scan(&t.ID, &t.UserID, &t.Name, &t.Scopes, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrBadToken
	}
	if err != nil {
		return nil, nil, err
	}
	if t.ExpiresAt != "" {
		if exp, err := time.Parse(time.RFC3339, t.ExpiresAt); err == nil && !s.now().Before(exp) {
			return nil, nil, ErrBadToken
		}
	}
	u, err := s.UserByID(ctx, t.UserID)
	if err != nil {
		return nil, nil, ErrBadToken
	}
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, t.ID)
	return u, &t, nil
}

// Tokens lists a user's tokens (all users when userID is empty).
func (s *Service) Tokens(ctx context.Context, userID string) ([]Token, error) {
	q := `SELECT id, user_id, name, scopes, last_used_at, expires_at, created_at FROM api_tokens WHERE server_id = ?`
	args := []any{s.st.ServerID}
	if userID != "" {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.st.DB.QueryContext(ctx, q+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Scopes, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err == nil {
			out = append(out, t)
		}
	}
	return out, nil
}

// RevokeToken deletes a token; non-admins may only delete their own.
func (s *Service) RevokeToken(ctx context.Context, userID, id string, admin bool) error {
	q, args := `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, []any{id, userID}
	if admin {
		q, args = `DELETE FROM api_tokens WHERE id = ?`, []any{id}
	}
	res, err := s.st.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBadToken
	}
	return nil
}

// writeScopes change the server. A viewer may never hold one, and only an
// admin may hold "shell", which is a root terminal.
var writeScopes = map[string]bool{"deploy": true, "cron": true, "db": true, "containers": true, "notify": true, "shell": true}

// CapScopes narrows a requested scope string to what the role may hold. An
// empty request means "everything the role allows", which is how the panel
// offers a token with no scopes ticked.
func CapScopes(requested, role string) (string, error) {
	if role == "admin" {
		return requested, nil
	}
	allowed := func(sc string) bool {
		if sc == "shell" {
			return false // admins only, and they returned above
		}
		if role == "viewer" {
			return !writeScopes[sc]
		}
		return true // deployer: everything except shell
	}
	if strings.TrimSpace(requested) == "" || requested == "*" {
		var keep []string
		for _, sc := range Scopes {
			if allowed(sc) {
				keep = append(keep, sc)
			}
		}
		return strings.Join(keep, ","), nil
	}
	var keep []string
	for _, sc := range strings.Split(requested, ",") {
		sc = strings.TrimSpace(sc)
		if sc == "" {
			continue
		}
		if !allowed(sc) {
			return "", errors.New("your role cannot create a token with the " + sc + " scope")
		}
		keep = append(keep, sc)
	}
	return strings.Join(keep, ","), nil
}

// Areas are the scopes that grant writing, one per part of the panel. A read is
// covered by "read" everywhere; these are what let a token change something, so
// an agent can be given exactly the ground it needs instead of everything.
//
// The order matters only where one prefix contains another, and none here does.
var areas = []struct{ prefix, scope string }{
	{"/api/v1/apps", "deploy"},
	{"/api/v1/deploy", "deploy"},
	{"/api/v1/cron", "cron"},
	{"/api/v1/databases", "db"},
	{"/api/v1/sql", "db"},
	{"/api/v1/docker", "containers"},
	{"/api/v1/domains", "domains"},
	{"/api/v1/proxy", "domains"},
	{"/api/v1/certificates", "domains"},
	{"/api/v1/backups", "backups"},
	{"/api/v1/security", "security"},
	{"/api/v1/uptime", "uptime"},
	{"/api/v1/runners", "runners"},
	{"/api/v1/catalog", "catalog"},
	{"/api/v1/recipes", "catalog"},
	{"/api/v1/workspaces", "workspaces"},
	{"/api/v1/logs", "logs"},
	{"/api/v1/system", "system"},
	{"/api/v1/metrics", "system"},
	{"/api/v1/health", "system"},
	{"/api/v1/attention", "system"},
	{"/api/v1/audit", "system"},
	{"/api/v1/commands", "system"},
	{"/api/v1/servers", "system"},
	{"/api/v1/fleet", "system"},
	{"/api/v1/settings", "settings"},
	{"/api/v1/sidebar", "settings"},
	// Turning the MCP server on or off is configuration, and a token that
	// could do it could switch off the thing it is talking through.
	{"/api/v1/mcp", "settings"},
	// The GitHub App's private key and the mail relay's credentials live
	// behind these, so they belong with the rest of the configuration.
	{"/api/v1/github", "settings"},
	{"/api/v1/mail", "settings"},
	{"/api/v1/diagnostics", "system"},
	{"/api/v1/dns-check", "system"},
	{"/api/v1/report", "system"},
}

// ScopeAllows reports whether a token's scopes cover a method and path.
//
// Deny by default, in both directions: a path nobody thought about is refused,
// and a scope only ever covers its own area. Nothing here grants a token the
// ability to mint another — /api/v1/auth stays closed to everything except the
// caller reading who they are, so no narrow scope can widen itself.
func ScopeAllows(scopes, method, path string) bool {
	if scopes == "*" {
		return true
	}
	has := func(sc string) bool {
		for _, s := range strings.Split(scopes, ",") {
			if strings.TrimSpace(s) == sc {
				return true
			}
		}
		return false
	}
	read := method == "GET" || method == "HEAD"
	switch {
	// These upgrade to a shell on the host or in a container. They are a GET
	// only because that is how a WebSocket starts, so they must never be
	// reachable with a read scope.
	case path == "/api/v1/terminal/ws",
		strings.HasSuffix(path, "/exec"),
		strings.HasSuffix(path, "/attach"):
		return has("shell")
	case path == "/mcp":
		return true
	// Sending a notification is a write that the read scope must not cover,
	// and it is the one thing under /api/v1/notify that "notify" is for.
	case path == "/api/v1/notify/emit":
		return has("notify")
	// Everything else under notify is channel configuration, which holds
	// webhook URLs and bot tokens. "notify" sends and does nothing else:
	// reading the configuration is a read, and changing it is configuration,
	// so it sits with the rest of the settings.
	case strings.HasPrefix(path, "/api/v1/notify"):
		if read {
			return has("read")
		}
		return has("settings")
	// Logs hang off many areas, so they are matched wherever they appear
	// rather than only under their own prefix.
	case strings.HasPrefix(path, "/api/v1/logs"), strings.Contains(path, "/logs"):
		// Every log route is a GET; there is nothing to write. The rule used
		// to grant any method, which was harmless only because no such route
		// existed — a poor thing for a permission check to rely on.
		return read && (has("logs") || has("read"))
	// Reading a file is reading anything on the server. The file API serves
	// whatever path the daemon can open, and the daemon is root, so a scope
	// that covered it would quietly include /etc/shadow, every .env an app was
	// deployed with and the daemon's own database. "read" means "look at the
	// state of the panel" everywhere else and must not mean this, so files
	// need their own scope in both directions.
	case strings.HasPrefix(path, "/api/v1/files"):
		return has("files")
	// Accounts and tokens. A token may read who it belongs to and nothing
	// else: no scope mints a token, so a narrow one cannot widen itself, and
	// "settings" deliberately stops short of this.
	case strings.HasPrefix(path, "/api/v1/auth/"), strings.HasPrefix(path, "/api/v1/users"):
		return path == "/api/v1/auth/me"
	}
	for _, a := range areas {
		if !strings.HasPrefix(path, a.prefix) {
			continue
		}
		if has(a.scope) {
			return true
		}
		return read && has("read")
	}
	// A path nobody thought about must not inherit the read scope simply
	// because it answers a GET.
	return false
}
