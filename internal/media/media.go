// Package media is the first Islet service an application calls rather than an
// operator.
//
// Everything else in this daemon answers to the person who owns the server. This
// answers to their software: an app POSTs an image and gets back a URL, a
// browser uploads a video straight to a bucket, a customer's phone fetches a
// thumbnail. None of those hold operator authority and none of them ever can —
// a media key is refused by the panel API and a panel session is refused here.
// That separation is the reason this is a package and not more routes.
//
// What it is for is the plumbing nobody wants to write twice: take a file,
// check it, put it somewhere that might be this disk or might be R2, remember
// what it was, and hand back a URL that can produce it at the size the page
// actually needs.
package media

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/scan"
	"github.com/isletdev/islet/internal/store"
)

// Defaults a server starts with. Each is a limit rather than a preference: a
// service that resizes on demand for anyone who asks is a service anyone can
// stop with a loop.
const (
	DefaultMaxBytes    = 256 << 20 // per object
	DefaultRatePerMin  = 120       // per key
	DefaultTransforms  = 1         // concurrent conversions on this machine
	DefaultSignedTTL   = 15 * time.Minute
	DefaultPresignTTL  = 15 * time.Minute
	derivativeInfix    = ".v"
	settingEnabled     = "media.enabled"
	settingHost        = "media.host"
	settingTransforms  = "media.transforms"
	settingMaxBytes    = "media.max_bytes"
	settingSigningSeed = "media.signing_seed"
)

var (
	ErrNotFound  = errors.New("no such thing")
	ErrDisabled  = errors.New("the media service is not enabled on this server")
	ErrForbidden = errors.New("that key may not do that")
	ErrTooBig    = errors.New("that file is larger than this bucket allows")
	ErrType      = errors.New("that content type is not allowed here")
	ErrQuota     = errors.New("this key is over its storage quota")
)

// Service is the media service.
type Service struct {
	st      *store.Store
	keys    *auth.Keys
	cmds    *cmdrun.Runner
	scanner *scan.Scanner
	dir     string
	log     *slog.Logger
	// gate bounds concurrent conversions. A buffered channel rather than a
	// worker pool: the only thing needed is a ceiling, and a ceiling of one is
	// the right default on the servers this runs on.
	gate chan struct{}
	// buckets keeps rate-limit state per key. Small, in memory, and lost on
	// restart, which is the correct amount of effort for a limit whose job is
	// to stop a loop rather than to be an accounting record.
	limiter *limiter
}

func New(st *store.Store, keys *auth.Keys, cmds *cmdrun.Runner, dataDir string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		st: st, keys: keys, cmds: cmds, scanner: scan.New(cmds, log),
		dir: filepath.Join(dataDir, "media"), log: log,
		gate:    make(chan struct{}, DefaultTransforms),
		limiter: newLimiter(),
	}
}

// ---- the service itself ---------------------------------------------------

// Settings is what the operator turned on.
type Settings struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	// Transforms is how many conversions may run at once on this machine.
	Transforms int   `json:"transforms"`
	MaxBytes   int64 `json:"maxBytes"`
}

func (s *Service) Settings(ctx context.Context) Settings {
	on, _, _ := s.st.Setting(ctx, settingEnabled)
	host, _, _ := s.st.Setting(ctx, settingHost)
	n, _, _ := s.st.Setting(ctx, settingTransforms)
	max, _, _ := s.st.Setting(ctx, settingMaxBytes)
	out := Settings{Enabled: on == "1", Host: host, Transforms: atoiOr(n, DefaultTransforms), MaxBytes: atoi64Or(max, DefaultMaxBytes)}
	return out
}

func (s *Service) SaveSettings(ctx context.Context, in Settings) error {
	if in.Transforms <= 0 || in.Transforms > 8 {
		in.Transforms = DefaultTransforms
	}
	if in.MaxBytes <= 0 {
		in.MaxBytes = DefaultMaxBytes
	}
	if err := s.st.SetSetting(ctx, settingEnabled, boolSetting(in.Enabled)); err != nil {
		return err
	}
	if err := s.st.SetSetting(ctx, settingHost, strings.TrimSpace(in.Host)); err != nil {
		return err
	}
	if err := s.st.SetSetting(ctx, settingTransforms, strconv.Itoa(in.Transforms)); err != nil {
		return err
	}
	if err := s.st.SetSetting(ctx, settingMaxBytes, strconv.FormatInt(in.MaxBytes, 10)); err != nil {
		return err
	}
	// Resize the ceiling to match, so changing it does not need a restart.
	s.gate = make(chan struct{}, in.Transforms)
	return nil
}

// Enabled is the one question every entry point asks first. A service that is
// off answers nothing, which is cheaper and clearer than a service that is off
// answering 404s from handlers that ran.
func (s *Service) Enabled(ctx context.Context) bool { return s.Settings(ctx).Enabled }

// ---- buckets --------------------------------------------------------------

// Bucket is a place bytes go.
type Bucket struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Driver      string            `json:"driver"`
	Config      map[string]string `json:"config"`
	AccessKey   string            `json:"accessKey,omitempty"`
	SecretSet   bool              `json:"secretSet"`
	PublicBase  string            `json:"publicBase"`
	MaxBytes    int64             `json:"maxBytes"`
	AllowTypes  string            `json:"allowTypes"`
	ScanUploads bool              `json:"scanUploads"`
	Default     bool              `json:"default"`
	CreatedAt   string            `json:"createdAt"`
	UpdatedAt   string            `json:"updatedAt"`
}

func (s *Service) Buckets(ctx context.Context) ([]Bucket, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, name, driver, config, access_key, secret_key, public_base, max_bytes, allow_types, scan_uploads, is_default, created_at, updated_at
		 FROM media_buckets WHERE server_id = ? ORDER BY is_default DESC, name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanBucket(sc interface{ Scan(...any) error }) (Bucket, error) {
	var b Bucket
	var cfg, secret string
	var scanUp, def int
	if err := sc.Scan(&b.ID, &b.Name, &b.Driver, &cfg, &b.AccessKey, &secret, &b.PublicBase, &b.MaxBytes, &b.AllowTypes, &scanUp, &def, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return b, err
	}
	b.Config = map[string]string{}
	_ = json.Unmarshal([]byte(cfg), &b.Config)
	b.SecretSet = secret != ""
	b.ScanUploads, b.Default = scanUp == 1, def == 1
	return b, nil
}

func (s *Service) Bucket(ctx context.Context, id string) (*Bucket, error) {
	row := s.st.DB.QueryRowContext(ctx,
		`SELECT id, name, driver, config, access_key, secret_key, public_base, max_bytes, allow_types, scan_uploads, is_default, created_at, updated_at
		 FROM media_buckets WHERE server_id = ? AND id = ?`, s.st.ServerID, id)
	b, err := scanBucket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

// DefaultBucket is where an upload goes when the key does not name one.
func (s *Service) DefaultBucket(ctx context.Context) (*Bucket, error) {
	list, err := s.Buckets(ctx)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// SaveBucket creates or updates one. A secret left empty on an update keeps the
// stored one, which is the only way to edit a bucket without pasting the key
// again.
func (s *Service) SaveBucket(ctx context.Context, actor string, b *Bucket, secret string) (*Bucket, error) {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return nil, errors.New("a bucket needs a name")
	}
	if b.Driver != "local" && b.Driver != "s3" {
		return nil, errors.New("driver must be local or s3")
	}
	if b.Driver == "s3" {
		for _, need := range []string{"endpoint", "bucket"} {
			if strings.TrimSpace(b.Config[need]) == "" {
				return nil, fmt.Errorf("an s3 bucket needs %s", need)
			}
		}
	}
	cfg, _ := json.Marshal(b.Config)
	sealed := ""
	if secret != "" {
		enc, err := s.keys.Encrypt([]byte(secret))
		if err != nil {
			return nil, err
		}
		sealed = base64.StdEncoding.EncodeToString(enc)
	}
	if b.ID == "" {
		b.ID = newID()
		if _, err := s.st.DB.ExecContext(ctx,
			`INSERT INTO media_buckets (id, server_id, name, driver, config, access_key, secret_key, public_base, max_bytes, allow_types, scan_uploads, is_default)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			b.ID, s.st.ServerID, b.Name, b.Driver, string(cfg), b.AccessKey, sealed, b.PublicBase, b.MaxBytes, b.AllowTypes, boolInt(b.ScanUploads), boolInt(b.Default)); err != nil {
			return nil, err
		}
	} else {
		q := `UPDATE media_buckets SET name=?, driver=?, config=?, access_key=?, public_base=?, max_bytes=?, allow_types=?, scan_uploads=?, is_default=?,
		      updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`
		args := []any{b.Name, b.Driver, string(cfg), b.AccessKey, b.PublicBase, b.MaxBytes, b.AllowTypes, boolInt(b.ScanUploads), boolInt(b.Default)}
		if sealed != "" {
			q += `, secret_key=?`
			args = append(args, sealed)
		}
		q += ` WHERE server_id=? AND id=?`
		args = append(args, s.st.ServerID, b.ID)
		if _, err := s.st.DB.ExecContext(ctx, q, args...); err != nil {
			return nil, err
		}
	}
	if b.Default {
		if _, err := s.st.DB.ExecContext(ctx,
			`UPDATE media_buckets SET is_default = 0 WHERE server_id = ? AND id <> ?`, s.st.ServerID, b.ID); err != nil {
			return nil, err
		}
	}
	_ = s.st.Audit(ctx, actor, "media.bucket.save", b.Name, b.Driver)
	return s.Bucket(ctx, b.ID)
}

// RemoveBucket refuses while objects point at it. Deleting the row would leave
// files nobody can reach and rows pointing at nothing.
func (s *Service) RemoveBucket(ctx context.Context, actor, id string) error {
	var n int
	if err := s.st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_objects WHERE server_id = ? AND bucket_id = ?`, s.st.ServerID, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%d objects are still in this bucket; delete them first", n)
	}
	b, err := s.Bucket(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM media_buckets WHERE server_id = ? AND id = ?`, s.st.ServerID, id); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "media.bucket.remove", b.Name, "")
	return nil
}

// storage builds the driver for a bucket.
func (s *Service) storage(ctx context.Context, b *Bucket) (Storage, error) {
	switch b.Driver {
	case "local":
		return &Local{Root: filepath.Join(s.dir, "objects", b.ID)}, nil
	case "s3":
		secret, err := s.bucketSecret(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		return &S3{
			Endpoint:  b.Config["endpoint"],
			Region:    orDefault(b.Config["region"], "auto"),
			Bucket:    b.Config["bucket"],
			Prefix:    b.Config["prefix"],
			AccessKey: b.AccessKey,
			SecretKey: secret,
			PathStyle: b.Config["pathStyle"] == "1" || b.Config["pathStyle"] == "true",
		}, nil
	}
	return nil, fmt.Errorf("unknown driver %q", b.Driver)
}

func (s *Service) bucketSecret(ctx context.Context, id string) (string, error) {
	var sealed string
	if err := s.st.DB.QueryRowContext(ctx,
		`SELECT secret_key FROM media_buckets WHERE server_id = ? AND id = ?`, s.st.ServerID, id).Scan(&sealed); err != nil {
		return "", err
	}
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	plain, err := s.keys.Decrypt(raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// CheckBucket proves a bucket works before an application depends on it: it
// writes a small object, reads it back, and removes it. A credential that is
// wrong should be found on the screen where it was typed.
func (s *Service) CheckBucket(ctx context.Context, id string) error {
	b, err := s.Bucket(ctx, id)
	if err != nil {
		return err
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return err
	}
	key := ".islet-check/" + newID()
	body := []byte("islet")
	if err := st.Put(ctx, key, strings.NewReader(string(body)), int64(len(body)), "text/plain"); err != nil {
		return err
	}
	defer func() { _ = st.Delete(context.WithoutCancel(ctx), key) }()
	rc, err := st.Get(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	got, err := io.ReadAll(io.LimitReader(rc, 32))
	if err != nil {
		return err
	}
	if string(got) != string(body) {
		return errors.New("the bucket returned something other than what was written")
	}
	return nil
}

// ---- presets --------------------------------------------------------------

// Preset is a size an application may ask for.
type Preset struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Fit     string `json:"fit"`
	Format  string `json:"format"`
	Quality int    `json:"quality"`
}

func (p Preset) width() int {
	if p.Width > 0 {
		return p.Width
	}
	if p.Height > 0 {
		return p.Height * 4 // vips needs a width; a tall box gets a generous one
	}
	return 1024
}

// ext is the file extension a variant of this preset gets.
func (p Preset) ext() string {
	switch p.Format {
	case "png":
		return ".png"
	case "avif":
		return ".avif"
	case "jpeg", "jpg":
		return ".jpg"
	default:
		return ".webp"
	}
}

func (p Preset) contentType() string {
	switch p.Format {
	case "png":
		return "image/png"
	case "avif":
		return "image/avif"
	case "jpeg", "jpg":
		return "image/jpeg"
	default:
		return "image/webp"
	}
}

func (s *Service) Presets(ctx context.Context) ([]Preset, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, name, width, height, fit, format, quality FROM media_presets WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Preset{}
	for rows.Next() {
		var p Preset
		if err := rows.Scan(&p.ID, &p.Name, &p.Width, &p.Height, &p.Fit, &p.Format, &p.Quality); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) Preset(ctx context.Context, name string) (*Preset, error) {
	var p Preset
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT id, name, width, height, fit, format, quality FROM media_presets WHERE server_id = ? AND name = ?`,
		s.st.ServerID, name).Scan(&p.ID, &p.Name, &p.Width, &p.Height, &p.Fit, &p.Format, &p.Quality)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

func (s *Service) SavePreset(ctx context.Context, actor string, p *Preset) (*Preset, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || strings.ContainsAny(p.Name, "/. ") {
		return nil, errors.New("a preset name is a word, without spaces, dots or slashes")
	}
	if p.Width <= 0 && p.Height <= 0 {
		return nil, errors.New("a preset needs a width or a height")
	}
	if p.Width > 8000 || p.Height > 8000 {
		return nil, errors.New("8000 pixels is as large as a preset goes")
	}
	if p.Fit != "contain" {
		p.Fit = "cover"
	}
	if p.Quality <= 0 || p.Quality > 100 {
		p.Quality = 80
	}
	if p.ID == "" {
		p.ID = newID()
		_, err := s.st.DB.ExecContext(ctx,
			`INSERT INTO media_presets (id, server_id, name, width, height, fit, format, quality) VALUES (?,?,?,?,?,?,?,?)`,
			p.ID, s.st.ServerID, p.Name, p.Width, p.Height, p.Fit, p.Format, p.Quality)
		if err != nil {
			return nil, err
		}
	} else if _, err := s.st.DB.ExecContext(ctx,
		`UPDATE media_presets SET name=?, width=?, height=?, fit=?, format=?, quality=? WHERE server_id=? AND id=?`,
		p.Name, p.Width, p.Height, p.Fit, p.Format, p.Quality, s.st.ServerID, p.ID); err != nil {
		return nil, err
	}
	_ = s.st.Audit(ctx, actor, "media.preset.save", p.Name, fmt.Sprintf("%dx%d %s", p.Width, p.Height, p.Format))
	return s.Preset(ctx, p.Name)
}

func (s *Service) RemovePreset(ctx context.Context, actor, id string) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM media_presets WHERE server_id = ? AND id = ?`, s.st.ServerID, id)
	if err == nil {
		_ = s.st.Audit(ctx, actor, "media.preset.remove", id, "")
	}
	return err
}

// ---- objects --------------------------------------------------------------

// Object is one uploaded thing.
type Object struct {
	ID          string            `json:"id"`
	BucketID    string            `json:"bucketId"`
	Namespace   string            `json:"namespace,omitempty"`
	Key         string            `json:"key"`
	Filename    string            `json:"filename"`
	ContentType string            `json:"contentType"`
	Size        int64             `json:"size"`
	Checksum    string            `json:"checksum,omitempty"`
	Width       int               `json:"width,omitempty"`
	Height      int               `json:"height,omitempty"`
	Visibility  string            `json:"visibility"`
	Scanner     string            `json:"scanner,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	KeyID       string            `json:"keyId,omitempty"`
	CreatedAt   string            `json:"createdAt"`
}

const objectCols = `id, bucket_id, namespace, object_key, filename, content_type, size_bytes, checksum, width, height, visibility, scanner, metadata, key_id, created_at`

func scanObject(sc interface{ Scan(...any) error }) (Object, error) {
	var o Object
	var meta string
	if err := sc.Scan(&o.ID, &o.BucketID, &o.Namespace, &o.Key, &o.Filename, &o.ContentType, &o.Size, &o.Checksum,
		&o.Width, &o.Height, &o.Visibility, &o.Scanner, &meta, &o.KeyID, &o.CreatedAt); err != nil {
		return o, err
	}
	o.Metadata = map[string]string{}
	_ = json.Unmarshal([]byte(meta), &o.Metadata)
	return o, nil
}

// Objects lists what is stored, newest first, optionally inside one namespace.
func (s *Service) Objects(ctx context.Context, namespace string, limit int) ([]Object, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + objectCols + ` FROM media_objects WHERE server_id = ?`
	args := []any{s.st.ServerID}
	if namespace != "" {
		q += ` AND namespace = ?`
		args = append(args, namespace)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.st.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Object{}
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Service) Object(ctx context.Context, id string) (*Object, error) {
	row := s.st.DB.QueryRowContext(ctx, `SELECT `+objectCols+` FROM media_objects WHERE server_id = ? AND id = ?`, s.st.ServerID, id)
	o, err := scanObject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &o, err
}

// Usage is what this service is costing, per namespace.
type Usage struct {
	Namespace string `json:"namespace"`
	Objects   int64  `json:"objects"`
	Bytes     int64  `json:"bytes"`
}

func (s *Service) Usage(ctx context.Context) ([]Usage, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT namespace, COUNT(*), COALESCE(SUM(size_bytes),0) FROM media_objects WHERE server_id = ? GROUP BY namespace ORDER BY 3 DESC`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Usage{}
	for rows.Next() {
		var u Usage
		if err := rows.Scan(&u.Namespace, &u.Objects, &u.Bytes); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ---- helpers --------------------------------------------------------------

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func boolSetting(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func atoiOr(s string, d int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
		return n
	}
	return d
}

func atoi64Or(s string, d int64) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
		return n
	}
	return d
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

// signingSeed is the secret behind signed URLs, made once and kept.
//
// Not the daemon's own key: a URL signature is handed out by the thousand and
// lives in logs, referrers and browser history, and the thing that seals
// credentials should not be within reach of any of that.
func (s *Service) signingSeed(ctx context.Context) ([]byte, error) {
	v, _, _ := s.st.Setting(ctx, settingSigningSeed)
	if v != "" {
		return base64.StdEncoding.DecodeString(v)
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	if err := s.st.SetSetting(ctx, settingSigningSeed, base64.StdEncoding.EncodeToString(seed)); err != nil {
		return nil, err
	}
	return seed, nil
}

// Sign produces the query string that makes a private URL work for a while.
func (s *Service) Sign(ctx context.Context, id, variant string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = DefaultSignedTTL
	}
	seed, err := s.signingSeed(ctx)
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(ttl).Unix()
	mac := hmac.New(sha256.New, seed)
	fmt.Fprintf(mac, "%s\n%s\n%d", id, variant, exp)
	return fmt.Sprintf("exp=%d&sig=%s", exp, base64.RawURLEncoding.EncodeToString(mac.Sum(nil))), nil
}

// Verify checks one. Constant time, and the expiry is checked after the
// signature so a stale link and a forged one take the same path.
func (s *Service) Verify(ctx context.Context, id, variant, exp, sig string) bool {
	seed, err := s.signingSeed(ctx)
	if err != nil {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, seed)
	fmt.Fprintf(mac, "%s\n%s\n%d", id, variant, n)
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return false
	}
	return time.Now().Unix() < n
}

// guessType is the content type of an upload, from the name rather than from
// what the uploader claimed. A browser will say whatever it likes.
func guessType(name, claimed string) string {
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(name))); t != "" {
		return strings.TrimSpace(strings.SplitN(t, ";", 2)[0])
	}
	claimed = strings.TrimSpace(strings.SplitN(claimed, ";", 2)[0])
	if claimed != "" {
		return claimed
	}
	return "application/octet-stream"
}

// sniffType is the last resort: what the first bytes say it is.
func sniffType(head []byte) string {
	return strings.TrimSpace(strings.SplitN(http.DetectContentType(head), ";", 2)[0])
}

// allowed checks a bucket's content-type allowlist. "image/*" means the family.
func allowed(list, contentType string) bool {
	list = strings.TrimSpace(list)
	if list == "" {
		return true
	}
	for _, want := range strings.Split(list, ",") {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		if strings.HasSuffix(want, "/*") {
			if strings.HasPrefix(contentType, strings.TrimSuffix(want, "*")) {
				return true
			}
			continue
		}
		if strings.EqualFold(want, contentType) {
			return true
		}
	}
	return false
}

// tmpFile is a staging file inside the directory the worker can see.
func (s *Service) tmpFile(prefix string) (*os.File, string, error) {
	if err := os.MkdirAll(s.workDir(), 0o750); err != nil {
		return nil, "", err
	}
	f, err := os.CreateTemp(s.workDir(), prefix+"-*")
	if err != nil {
		return nil, "", err
	}
	return f, filepath.Base(f.Name()), nil
}
