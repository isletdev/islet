// Package vault stores secrets under a name, so the same value can be referred
// to from an app, a cron job and a backup hook without being pasted into three
// places and rotated in three places.
//
// The value is encrypted with the daemon's key, the way every other credential
// in Islet already is. What this package adds is the name, the substitution,
// and the rule that reading a value back is a deliberate act that gets written
// down.
package vault

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/store"
)

// Keys is the daemon's encryption, kept as an interface so this package can be
// tested without one.
type Keys interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

// Secret is what the panel sees. There is no value field, deliberately: a list
// that carried the values would put every secret on the server into any log,
// screenshot or browser cache that happened to catch the response.
type Secret struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	LastUsedAt  string `json:"lastUsedAt,omitempty"`
}

// nameRe is what a reference can contain. Upper case with underscores, because
// that is what an environment variable looks like and referring to one is the
// point.
var nameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// ref matches a reference to a secret in a value: @vault:NAME.
var ref = regexp.MustCompile(`@vault:([A-Z][A-Z0-9_]{0,63})`)

// Service owns the table.
type Service struct {
	st   *store.Store
	keys Keys
	now  func() time.Time
}

func New(st *store.Store, keys Keys) *Service {
	return &Service{st: st, keys: keys, now: time.Now}
}

// ValidName reports whether a name may be used, and says why not when it may
// not — a rejected name with no reason is the most annoying kind.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return errors.New("a name is 1 to 64 characters, upper case letters, digits and underscores, starting with a letter — like DATABASE_PASSWORD")
	}
	return nil
}

// List returns every secret's name and description, never a value.
func (s *Service) List(ctx context.Context) ([]Secret, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, name, description, created_at, updated_at, COALESCE(last_used_at, '')
		 FROM vault_secrets WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Secret{}
	for rows.Next() {
		var v Secret
		if err := rows.Scan(&v.ID, &v.Name, &v.Description, &v.CreatedAt, &v.UpdatedAt, &v.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Set stores a value under a name, replacing whatever was there.
func (s *Service) Set(ctx context.Context, name, value, description string) error {
	name = strings.TrimSpace(name)
	if err := ValidName(name); err != nil {
		return err
	}
	if value == "" {
		return errors.New("a secret needs a value; delete it instead of emptying it")
	}
	enc, err := s.keys.Encrypt([]byte(value))
	if err != nil {
		return err
	}
	id, err := newID()
	if err != nil {
		return err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err = s.st.DB.ExecContext(ctx,
		`INSERT INTO vault_secrets (id, server_id, name, value_enc, description, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (server_id, name) DO UPDATE SET
		   value_enc = excluded.value_enc, description = excluded.description, updated_at = excluded.updated_at`,
		id, s.st.ServerID, name, enc, description, now, now)
	return err
}

// Delete removes one.
func (s *Service) Delete(ctx context.Context, name string) error {
	res, err := s.st.DB.ExecContext(ctx, `DELETE FROM vault_secrets WHERE server_id = ? AND name = ?`, s.st.ServerID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no secret called %s", name)
	}
	return nil
}

// Value reads one back. Every caller of this is either substituting into a
// process or answering a deliberate request to see it, and both are worth
// recording — which is why the caller does the recording rather than this.
func (s *Service) Value(ctx context.Context, name string) (string, error) {
	var enc []byte
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT value_enc FROM vault_secrets WHERE server_id = ? AND name = ?`, s.st.ServerID, name).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no secret called %s", name)
	}
	if err != nil {
		return "", err
	}
	b, err := s.keys.Decrypt(enc)
	if err != nil {
		return "", fmt.Errorf("could not decrypt %s: %w", name, err)
	}
	_, _ = s.st.DB.ExecContext(ctx,
		`UPDATE vault_secrets SET last_used_at = ? WHERE server_id = ? AND name = ?`,
		s.now().UTC().Format(time.RFC3339Nano), s.st.ServerID, name)
	return string(b), nil
}

// Expand replaces every @vault:NAME in a string with its value.
//
// A name that does not exist is left exactly as written rather than replaced
// with nothing. An empty password is a working connection string to the wrong
// database; @vault:TYPO is a value that fails immediately and says what is
// wrong when anyone looks at it.
func (s *Service) Expand(ctx context.Context, in string) (string, []string, error) {
	names := Refs(in)
	if len(names) == 0 {
		return in, nil, nil
	}
	var missing []string
	out := in
	for _, n := range names {
		v, err := s.Value(ctx, n)
		if err != nil {
			missing = append(missing, n)
			continue
		}
		out = strings.ReplaceAll(out, "@vault:"+n, v)
	}
	sort.Strings(missing)
	return out, missing, nil
}

// newID is local rather than borrowed from store, which keeps its own
// unexported. Six bytes is plenty for a row nobody types by hand.
func newID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Refs lists the secrets a string refers to, in the order they first appear.
func Refs(in string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range ref.FindAllStringSubmatch(in, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
