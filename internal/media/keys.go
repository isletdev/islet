package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// A key is what an application holds.
//
// It looks like an API token and is deliberately not one. An API token carries
// the operator's authority narrowed by scopes; this carries none, is held by
// software reachable from the open internet, and is refused by the panel API.
// Two things that look alike and must never be the same row — which is why they
// are not, and why the prefix is different enough to tell apart in a log.

// KeyPrefix marks a media key on sight. Seeing one in an application's
// environment file should say immediately what it is and what it is not.
const KeyPrefix = "islet_media_"

// Key is an application's credential.
type Key struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Namespace  string `json:"namespace"`
	BucketID   string `json:"bucketId,omitempty"`
	Scopes     string `json:"scopes"`
	RatePerMin int    `json:"ratePerMin"`
	QuotaBytes int64  `json:"quotaBytes"`
	Origins    string `json:"origins"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	CreatedAt  string `json:"createdAt"`
	// Secret is filled in exactly once, by Mint, and never read back out of the
	// database — because it is not in the database.
	Secret string `json:"secret,omitempty"`
}

// Can reports whether this key may do something.
func (k *Key) Can(scope string) bool {
	for _, s := range strings.Split(k.Scopes, ",") {
		if strings.TrimSpace(s) == scope {
			return true
		}
	}
	return false
}

// AllowsOrigin reports whether a browser at this origin may use the key.
//
// Empty means no browser may, which is the safer default to have: a key with no
// origins is a server-side key, and a server-side key leaking into a web page
// should stop working rather than keep going.
func (k *Key) AllowsOrigin(origin string) bool {
	origin = normalOrigin(origin)
	if origin == "" {
		return true // not a browser request
	}
	for _, o := range strings.Split(k.Origins, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" || strings.EqualFold(normalOrigin(o), origin) {
			return true
		}
	}
	return false
}

// normalOrigin makes a typed address comparable to a header.
//
// An Origin header has no path, so it never ends in a slash — but somebody
// filling in a form copies the address bar, and the address bar has one. Stored
// as "https://shop.example/" it matched nothing a browser ever sent, and the
// upload was refused with no hint as to why. The slash is not a difference
// worth having, so it is not one.
func normalOrigin(o string) string {
	return strings.TrimRight(strings.TrimSpace(o), "/")
}

// OriginAllowedByAnyKey answers a preflight, which cannot be answered any other
// way.
//
// A CORS preflight is sent by the browser before the real request and carries
// no Authorization header — that is the whole point of it, and it is why asking
// "which key is this?" at preflight time returns nothing. Answering from the
// keys as a whole is the only thing that can work, and it gives nothing away:
// the preflight only tells a browser it may attempt the request. The request
// itself still carries the key and is still checked against that key's own
// origins, which is where the decision belongs.
func (s *Service) OriginAllowedByAnyKey(ctx context.Context, origin string) bool {
	if normalOrigin(origin) == "" {
		return false
	}
	keys, err := s.Keys(ctx)
	if err != nil {
		return false
	}
	for i := range keys {
		if keys[i].Origins != "" && keys[i].AllowsOrigin(origin) {
			return true
		}
	}
	return false
}

const keyCols = `id, name, prefix, namespace, bucket_id, scopes, rate_per_min, quota_bytes, origins, expires_at, last_used_at, created_at`

func scanKey(sc interface{ Scan(...any) error }) (Key, error) {
	var k Key
	err := sc.Scan(&k.ID, &k.Name, &k.Prefix, &k.Namespace, &k.BucketID, &k.Scopes, &k.RatePerMin, &k.QuotaBytes,
		&k.Origins, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt)
	return k, err
}

func (s *Service) Keys(ctx context.Context) ([]Key, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+keyCols+` FROM media_keys WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Mint creates a key and returns it with its secret, the only time that
// happens. Stored as a SHA-256 of the secret: a stolen database is not a set of
// working credentials.
func (s *Service) Mint(ctx context.Context, actor string, k *Key) (*Key, error) {
	k.Name = strings.TrimSpace(k.Name)
	if k.Name == "" {
		return nil, errors.New("a key needs a name")
	}
	if strings.TrimSpace(k.Scopes) == "" {
		k.Scopes = "upload,read"
	}
	for _, sc := range strings.Split(k.Scopes, ",") {
		switch strings.TrimSpace(sc) {
		case "upload", "read", "delete", "sign":
		default:
			return nil, errors.New("scopes are any of upload, read, delete, sign")
		}
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	secret := KeyPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(secret))
	k.ID = newID()
	k.Prefix = secret[:len(KeyPrefix)+6]
	if _, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO media_keys (id, server_id, name, prefix, hash, namespace, bucket_id, scopes, rate_per_min, quota_bytes, origins, expires_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		k.ID, s.st.ServerID, k.Name, k.Prefix, hex.EncodeToString(sum[:]), k.Namespace, k.BucketID, k.Scopes,
		k.RatePerMin, k.QuotaBytes, k.Origins, k.ExpiresAt); err != nil {
		return nil, err
	}
	_ = s.st.Audit(ctx, actor, "media.key.create", k.Name, k.Scopes+" ns="+k.Namespace)
	out, err := s.keyByID(ctx, k.ID)
	if err != nil {
		return nil, err
	}
	out.Secret = secret
	return out, nil
}

func (s *Service) keyByID(ctx context.Context, id string) (*Key, error) {
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+keyCols+` FROM media_keys WHERE server_id = ? AND id = ?`, s.st.ServerID, id)
	k, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &k, err
}

func (s *Service) RemoveKey(ctx context.Context, actor, id string) error {
	k, err := s.keyByID(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM media_keys WHERE server_id = ? AND id = ?`, s.st.ServerID, id); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "media.key.remove", k.Name, "")
	return nil
}

// Authenticate turns a presented secret into the key it belongs to.
//
// Looked up by hash, not by prefix: the prefix is for a human reading a list,
// and matching on it would make a key guessable six characters at a time.
func (s *Service) Authenticate(ctx context.Context, secret string) (*Key, error) {
	secret = strings.TrimSpace(secret)
	if !strings.HasPrefix(secret, KeyPrefix) {
		return nil, ErrForbidden
	}
	sum := sha256.Sum256([]byte(secret))
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+keyCols+` FROM media_keys WHERE server_id = ? AND hash = ?`,
		s.st.ServerID, hex.EncodeToString(sum[:]))
	k, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if k.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, k.ExpiresAt); err == nil && time.Now().After(t) {
			return nil, ErrForbidden
		}
	}
	// Written without cancelling on the request: knowing a key is in use is
	// worth a write even when the caller has gone.
	go func() {
		_, _ = s.st.DB.ExecContext(context.Background(),
			`UPDATE media_keys SET last_used_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, k.ID)
	}()
	return &k, nil
}

// StoredBytes is what this key has put in the bucket, for its quota.
func (s *Service) StoredBytes(ctx context.Context, keyID string) (int64, error) {
	var n sql.NullInt64
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT SUM(size_bytes) FROM media_objects WHERE server_id = ? AND key_id = ?`, s.st.ServerID, keyID).Scan(&n)
	return n.Int64, err
}

// ---- rate limiting --------------------------------------------------------

// limiter is one token bucket per key.
//
// In memory and lost on restart, which is the right amount of effort: its job is
// to stop a loop, not to be an accounting record. A restart that forgives a
// minute of requests costs nothing; a table that records every request on a
// 1 vCPU server costs the thing it is protecting.
type limiter struct {
	mu sync.Mutex
	at map[string]*bucketState
}

type bucketState struct {
	tokens float64
	last   time.Time
}

func newLimiter() *limiter { return &limiter{at: map[string]*bucketState{}} }

// allow takes a token for id, refilling at perMin a minute with a burst of a
// tenth of that — enough for a page loading a dozen images at once.
func (l *limiter) allow(id string, perMin int) bool {
	if perMin <= 0 {
		perMin = DefaultRatePerMin
	}
	burst := float64(perMin) / 6
	if burst < 5 {
		burst = 5
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.at[id]
	if b == nil {
		b = &bucketState{tokens: burst, last: now}
		l.at[id] = b
		// Cheap eviction: a server with thousands of keys is not the case this
		// is for, and a map that only grows is a leak however slow.
		if len(l.at) > 10000 {
			for k, v := range l.at {
				if now.Sub(v.last) > time.Hour {
					delete(l.at, k)
				}
			}
		}
	}
	b.tokens += now.Sub(b.last).Minutes() * float64(perMin)
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// AnonRatePerMin bounds what one address may ask for without a key.
//
// Public objects are read without one — that is what public means. Deliberately
// generous, and deliberately not the thing this relies on: behind the proxy
// Islet already runs, every request arrives from the same address, so this is
// one shared ceiling rather than a per-visitor one. Trusting X-Forwarded-For
// instead would make it a limit anybody can step around by changing a header,
// which is worse than a blunt one.
//
// What actually protects the expensive path is elsewhere and is not blunt: a
// cached variant is a file read or a redirect, and a cache miss has to wait for
// one of a small number of conversion slots or be refused.
const AnonRatePerMin = 1200

// AllowAnon takes a token for an address rather than a key.
func (s *Service) AllowAnon(addr string) bool {
	return s.limiter.allow("ip:"+addr, AnonRatePerMin)
}

// Allow takes a token for this key. The service's own limit applies when the
// key does not set one, because "no limit configured" must never mean "no
// limit" on an endpoint the internet can reach.
func (s *Service) Allow(k *Key) bool {
	return s.limiter.allow(k.ID, k.RatePerMin)
}
