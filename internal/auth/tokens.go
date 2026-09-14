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

// Scopes a token can carry. "*" grants everything the user can do.
// Scopes a token may carry. "shell" is root on the server: it opens the host
// terminal and container exec, so it is deliberately separate from "read".
var Scopes = []string{"read", "deploy", "cron", "notify", "logs", "db", "containers", "shell"}

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

// ScopeAllows reports whether a token's scopes cover a method and path.
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
	case strings.HasPrefix(path, "/api/v1/apps"):
		return has("deploy") || (read && has("read"))
	case strings.HasPrefix(path, "/api/v1/cron"):
		return has("cron") || (read && has("read"))
	case path == "/api/v1/notify/emit":
		return has("notify")
	case strings.HasPrefix(path, "/api/v1/notify"):
		return read && has("read")
	case strings.HasPrefix(path, "/api/v1/logs"), strings.Contains(path, "/logs"):
		return has("logs") || has("read")
	case strings.HasPrefix(path, "/api/v1/databases"):
		return has("db") || (read && has("read"))
	case strings.HasPrefix(path, "/api/v1/docker"):
		return has("containers") || (read && has("read"))
	case strings.HasPrefix(path, "/api/v1/auth/tokens"), strings.HasPrefix(path, "/api/v1/auth/"):
		return path == "/api/v1/auth/me"
	// Plain reads of the server's own state.
	case strings.HasPrefix(path, "/api/v1/health"),
		strings.HasPrefix(path, "/api/v1/system"),
		strings.HasPrefix(path, "/api/v1/metrics"),
		strings.HasPrefix(path, "/api/v1/domains"),
		strings.HasPrefix(path, "/api/v1/proxy"),
		strings.HasPrefix(path, "/api/v1/catalog"),
		strings.HasPrefix(path, "/api/v1/uptime"),
		strings.HasPrefix(path, "/api/v1/backups"),
		strings.HasPrefix(path, "/api/v1/security"),
		strings.HasPrefix(path, "/api/v1/runners"),
		strings.HasPrefix(path, "/api/v1/recipes"),
		strings.HasPrefix(path, "/api/v1/attention"),
		strings.HasPrefix(path, "/api/v1/audit"),
		strings.HasPrefix(path, "/api/v1/commands"),
		strings.HasPrefix(path, "/api/v1/files"),
		strings.HasPrefix(path, "/api/v1/settings"):
		return read && has("read")
	default:
		// Deny by default. A path nobody thought about must not inherit the
		// read scope simply because it answers a GET.
		return false
	}
}
