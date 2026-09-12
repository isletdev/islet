// Package auth implements users, sessions, passwords and two-factor login.
// It knows nothing about HTTP; internal/api adapts it.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/store"
)

const (
	// SessionTTL is the fixed lifetime of a session cookie.
	SessionTTL = 7 * 24 * time.Hour
	// Issuer is shown in authenticator apps.
	Issuer = "Islet"
	// recoveryCodeCount is how many one-time codes 2FA setup hands out.
	recoveryCodeCount = 10
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrRateLimited        = errors.New("too many attempts")
	ErrSetupDone          = errors.New("setup already completed")
	ErrBadSetupToken      = errors.New("setup token is wrong")
	ErrNoSession          = errors.New("no session")
	ErrMFARequired        = errors.New("second factor required")
	ErrBadCode            = errors.New("code is not valid")
	ErrTOTPNotPending     = errors.New("no pending two-factor setup")
	ErrTOTPAlreadyOn      = errors.New("two-factor authentication is already enabled")
	ErrTOTPOff            = errors.New("two-factor authentication is not enabled")

	usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)
)

// User is a panel account.
type User struct {
	ID          string
	Username    string
	Role        string
	Projects    string // comma list of app-name globs for deployers and viewers; empty = all
	TOTPEnabled bool
	CreatedAt   string
	LastLoginAt string
}

// Session is a logged-in browser.
type Session struct {
	ID         string
	UserID     string
	MFAPending bool
	IP         string
	UserAgent  string
	CreatedAt  string
	LastSeenAt string
	ExpiresAt  time.Time
}

// Service is the auth facade.
type Service struct {
	st        *store.Store
	keys      *Keys
	dataDir   string
	loginRate *Limiter
	now       func() time.Time
}

// New builds the service and, on a fresh install, creates the setup token.
func New(st *store.Store, keys *Keys, dataDir string) (*Service, error) {
	s := &Service{st: st, keys: keys, dataDir: dataDir, loginRate: NewLimiter(10, 15*time.Minute), now: time.Now}
	needs, err := s.NeedsSetup(context.Background())
	if err != nil {
		return nil, err
	}
	if needs {
		if _, err := s.ensureSetupToken(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// ---- setup ----

func (s *Service) setupTokenPath() string { return filepath.Join(s.dataDir, "setup-token") }

// NeedsSetup is true until the first admin exists.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	var n int
	if err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// SetupToken returns the one-time token that gates first-run setup.
func (s *Service) SetupToken() (string, error) { return s.ensureSetupToken() }

func (s *Service) ensureSetupToken() (string, error) {
	raw, err := os.ReadFile(s.setupTokenPath())
	if err == nil && len(strings.TrimSpace(string(raw))) == 64 {
		return strings.TrimSpace(string(raw)), nil
	}
	tok, err := randomHex(32)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(s.setupTokenPath(), []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write setup token: %w", err)
	}
	return tok, nil
}

// CompleteSetup creates the first admin. It is the only way to create a user
// while there are none, and it requires the setup token.
func (s *Service) CompleteSetup(ctx context.Context, token, username, password string) (*User, error) {
	needs, err := s.NeedsSetup(ctx)
	if err != nil {
		return nil, err
	}
	if !needs {
		return nil, ErrSetupDone
	}
	want, err := s.ensureSetupToken()
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(want)) != 1 {
		return nil, ErrBadSetupToken
	}
	u, err := s.createUser(ctx, username, password, "admin")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(s.setupTokenPath())
	_ = s.st.Audit(ctx, u.Username, "auth.setup", u.ID, "first admin created")
	return u, nil
}

func (s *Service) createUser(ctx context.Context, username, password, role string) (*User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernameRe.MatchString(username) {
		return nil, errors.New("username must be 3 to 32 characters: lowercase letters, digits, dot, dash or underscore")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	if _, err := s.st.DB.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)`,
		id, username, hash, role); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("username is taken")
		}
		return nil, err
	}
	return s.UserByID(ctx, id)
}

// ---- users ----

// Users lists every account.
func (s *Service) Users(ctx context.Context) ([]User, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, username, role, totp_secret_enc, created_at, last_login_at, projects FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		var totp []byte
		var last sql.NullString
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &totp, &u.CreatedAt, &last, &u.Projects); err != nil {
			return nil, err
		}
		u.TOTPEnabled = len(totp) > 0
		u.LastLoginAt = last.String
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser adds an account with a role.
func (s *Service) CreateUser(ctx context.Context, username, password, role string) (*User, error) {
	if role != "admin" && role != "deployer" && role != "viewer" {
		return nil, errors.New("role must be admin, deployer or viewer")
	}
	return s.createUser(ctx, username, password, role)
}

// UpdateUser changes a role and/or password. The last admin cannot be
// demoted, and an admin cannot demote themselves.
func (s *Service) UpdateUser(ctx context.Context, id, role, password, actorID string) error {
	if role != "" {
		if role != "admin" && role != "deployer" && role != "viewer" {
			return errors.New("role must be admin, deployer or viewer")
		}
		if id == actorID && role != "admin" {
			return errors.New("you cannot remove your own admin role")
		}
		if role != "admin" {
			var admins int
			_ = s.st.DB.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND id <> ?`, id).Scan(&admins)
			if admins == 0 {
				return errors.New("at least one admin must remain")
			}
		}
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id); err != nil {
			return err
		}
	}
	if password != "" {
		hash, err := HashPassword(password)
		if err != nil {
			return err
		}
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id); err != nil {
			return err
		}
		_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
	}
	return nil
}

// DeleteUser removes an account and its sessions and tokens.
func (s *Service) DeleteUser(ctx context.Context, id, actorID string) error {
	if id == actorID {
		return errors.New("you cannot delete your own account")
	}
	var admins int
	_ = s.st.DB.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND id <> ?`, id).Scan(&admins)
	if admins == 0 {
		return errors.New("at least one admin must remain")
	}
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE user_id = ?`, id)
	res, err := s.st.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("user not found")
	}
	return nil
}

// UserByID loads a user.
func (s *Service) UserByID(ctx context.Context, id string) (*User, error) {
	var u User
	var lastLogin sql.NullString
	var totp []byte
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, username, role, totp_secret_enc, created_at, last_login_at, projects FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.Role, &totp, &u.CreatedAt, &lastLogin, &u.Projects)
	if err != nil {
		return nil, err
	}
	u.TOTPEnabled = len(totp) > 0
	u.LastLoginAt = lastLogin.String
	return &u, nil
}

// ChangePassword verifies the current password and sets a new one. All other
// sessions of the user are revoked.
func (s *Service) ChangePassword(ctx context.Context, userID, current, next, keepSessionID string) error {
	var hash string
	if err := s.st.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		return err
	}
	if !VerifyPassword(hash, current) {
		return ErrInvalidCredentials
	}
	newHash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, newHash, userID); err != nil {
		return err
	}
	_, err = s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keepSessionID)
	_ = s.st.Audit(ctx, userID, "auth.password_changed", userID, "")
	return err
}

// ---- login and sessions ----

// Login checks credentials and opens a session. When the user has 2FA, the
// session starts as MFA-pending and VerifySecondFactor must complete it.
func (s *Service) Login(ctx context.Context, username, password, ip, userAgent string) (token string, sess *Session, err error) {
	now := s.now()
	username = strings.ToLower(strings.TrimSpace(username))
	ipKey, userKey := "ip:"+ip, "user:"+username
	if !s.loginRate.Allow(ipKey, now) || !s.loginRate.Allow(userKey, now) {
		return "", nil, ErrRateLimited
	}
	var id, hash string
	var totp []byte
	err = s.st.DB.QueryRowContext(ctx, `SELECT id, password_hash, totp_secret_enc FROM users WHERE username = ?`, username).Scan(&id, &hash, &totp)
	if errors.Is(err, sql.ErrNoRows) {
		// Burn the same time as a real verification so usernames are not enumerable.
		VerifyPassword("$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		s.loginRate.Fail(ipKey, now)
		s.loginRate.Fail(userKey, now)
		_ = s.st.Audit(ctx, username, "auth.login_failed", "", "ip="+ip)
		return "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", nil, err
	}
	if !VerifyPassword(hash, password) {
		s.loginRate.Fail(ipKey, now)
		s.loginRate.Fail(userKey, now)
		_ = s.st.Audit(ctx, username, "auth.login_failed", id, "ip="+ip)
		return "", nil, ErrInvalidCredentials
	}
	s.loginRate.Reset(ipKey)
	s.loginRate.Reset(userKey)
	pending := len(totp) > 0
	token, sess, err = s.createSession(ctx, id, pending, ip, userAgent)
	if err != nil {
		return "", nil, err
	}
	if !pending {
		s.markLogin(ctx, id, username, ip)
	}
	return token, sess, nil
}

// RetryAfter reports how long a rate-limited client should wait.
func (s *Service) RetryAfter(username, ip string) time.Duration {
	now := s.now()
	a := s.loginRate.RetryAfter("ip:"+ip, now)
	b := s.loginRate.RetryAfter("user:"+strings.ToLower(strings.TrimSpace(username)), now)
	if b > a {
		return b
	}
	return a
}

func (s *Service) markLogin(ctx context.Context, userID, username, ip string) {
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE users SET last_login_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, userID)
	_ = s.st.Audit(ctx, username, "auth.login", userID, "ip="+ip)
}

func (s *Service) createSession(ctx context.Context, userID string, mfaPending bool, ip, ua string) (string, *Session, error) {
	token, err := randomHex(32)
	if err != nil {
		return "", nil, err
	}
	id, err := randomHex(8)
	if err != nil {
		return "", nil, err
	}
	exp := s.now().Add(SessionTTL).UTC()
	if len(ua) > 256 {
		ua = ua[:256]
	}
	if _, err := s.st.DB.ExecContext(ctx, `INSERT INTO sessions (id, token_hash, user_id, mfa_pending, ip, user_agent, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, hashToken(token), userID, boolInt(mfaPending), ip, ua, exp.Format(time.RFC3339)); err != nil {
		return "", nil, err
	}
	return token, &Session{ID: id, UserID: userID, MFAPending: mfaPending, IP: ip, UserAgent: ua, ExpiresAt: exp}, nil
}

// SessionByToken resolves a cookie value to a live session, touching last_seen.
func (s *Service) SessionByToken(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, ErrNoSession
	}
	var sess Session
	var pending int
	var exp string
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, user_id, mfa_pending, ip, user_agent, created_at, last_seen_at, expires_at
		FROM sessions WHERE token_hash = ?`, hashToken(token)).
		Scan(&sess.ID, &sess.UserID, &pending, &sess.IP, &sess.UserAgent, &sess.CreatedAt, &sess.LastSeenAt, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	sess.ExpiresAt, _ = time.Parse(time.RFC3339, exp)
	if !s.now().Before(sess.ExpiresAt) {
		_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID)
		return nil, ErrNoSession
	}
	sess.MFAPending = pending == 1
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, sess.ID)
	return &sess, nil
}

// Logout deletes the session behind a cookie value.
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// RevokeOthers deletes every session except keep and every API token.
func (s *Service) RevokeOthers(ctx context.Context, keep string) error {
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id <> ?`, keep); err != nil {
		return err
	}
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM api_tokens`)
	return err
}

// AllAdminsHave2FA reports whether every admin has TOTP enabled.
func (s *Service) AllAdminsHave2FA(ctx context.Context) bool {
	var n int
	if err := s.st.DB.QueryRowContext(ctx, `SELECT count(*) FROM users u WHERE u.role = 'admin' AND (u.totp_secret_enc IS NULL OR length(u.totp_secret_enc) = 0)`).Scan(&n); err != nil {
		return false
	}
	return n == 0
}

// Sessions lists a user's live sessions.
func (s *Service) Sessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, user_id, mfa_pending, ip, user_agent, created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC`, userID, s.now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var sess Session
		var pending int
		var exp string
		if err := rows.Scan(&sess.ID, &sess.UserID, &pending, &sess.IP, &sess.UserAgent, &sess.CreatedAt, &sess.LastSeenAt, &exp); err != nil {
			return nil, err
		}
		sess.MFAPending = pending == 1
		sess.ExpiresAt, _ = time.Parse(time.RFC3339, exp)
		out = append(out, sess)
	}
	return out, rows.Err()
}

// RevokeSession deletes one of the user's sessions by id.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, sessionID, userID)
	return err
}

// PurgeExpired removes dead sessions; call it periodically.
func (s *Service) PurgeExpired(ctx context.Context) {
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, s.now().UTC().Format(time.RFC3339))
}

// ---- second factor ----

// VerifySecondFactor completes an MFA-pending session with a TOTP code or a
// recovery code.
func (s *Service) VerifySecondFactor(ctx context.Context, sess *Session, code string, ip string) error {
	if !sess.MFAPending {
		return nil
	}
	now := s.now()
	key := "mfa:" + sess.UserID
	if !s.loginRate.Allow(key, now) {
		return ErrRateLimited
	}
	code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
	ok := false
	if len(code) == totpDigits {
		ok = s.checkTOTP(ctx, sess.UserID, code, now)
	} else {
		ok = s.consumeRecoveryCode(ctx, sess.UserID, code)
	}
	if !ok {
		s.loginRate.Fail(key, now)
		_ = s.st.Audit(ctx, sess.UserID, "auth.mfa_failed", sess.UserID, "ip="+ip)
		return ErrBadCode
	}
	s.loginRate.Reset(key)
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE sessions SET mfa_pending = 0 WHERE id = ?`, sess.ID); err != nil {
		return err
	}
	sess.MFAPending = false
	u, _ := s.UserByID(ctx, sess.UserID)
	if u != nil {
		s.markLogin(ctx, u.ID, u.Username, ip)
	}
	return nil
}

func (s *Service) checkTOTP(ctx context.Context, userID, code string, now time.Time) bool {
	var enc []byte
	var last int64
	if err := s.st.DB.QueryRowContext(ctx, `SELECT totp_secret_enc, totp_last_step FROM users WHERE id = ?`, userID).Scan(&enc, &last); err != nil || len(enc) == 0 {
		return false
	}
	secret, err := s.keys.Decrypt(enc)
	if err != nil {
		return false
	}
	step, ok := VerifyTOTP(string(secret), code, now, last)
	if !ok {
		return false
	}
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE users SET totp_last_step = ? WHERE id = ?`, step, userID)
	return true
}

// BeginTOTPSetup generates a secret and stores it as pending until enabled.
func (s *Service) BeginTOTPSetup(ctx context.Context, userID string) (secret, otpauth string, err error) {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if u.TOTPEnabled {
		return "", "", ErrTOTPAlreadyOn
	}
	secret, err = NewTOTPSecret()
	if err != nil {
		return "", "", err
	}
	enc, err := s.keys.Encrypt([]byte(secret))
	if err != nil {
		return "", "", err
	}
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE users SET totp_pending_enc = ? WHERE id = ?`, enc, userID); err != nil {
		return "", "", err
	}
	return secret, OTPAuthURL(secret, u.Username, Issuer), nil
}

// EnableTOTP confirms the pending secret with a code and returns recovery
// codes. They are shown once; only hashes are stored.
func (s *Service) EnableTOTP(ctx context.Context, userID, code string) ([]string, error) {
	var pending []byte
	if err := s.st.DB.QueryRowContext(ctx, `SELECT totp_pending_enc FROM users WHERE id = ?`, userID).Scan(&pending); err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, ErrTOTPNotPending
	}
	secret, err := s.keys.Decrypt(pending)
	if err != nil {
		return nil, err
	}
	step, ok := VerifyTOTP(string(secret), code, s.now(), 0)
	if !ok {
		return nil, ErrBadCode
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET totp_secret_enc = totp_pending_enc, totp_pending_enc = NULL, totp_last_step = ? WHERE id = ?`, step, userID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)`, userID, h); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = s.st.Audit(ctx, userID, "auth.totp_enabled", userID, "")
	return codes, nil
}

// DisableTOTP turns 2FA off after a valid code (TOTP or recovery).
func (s *Service) DisableTOTP(ctx context.Context, userID, code string) error {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return ErrTOTPOff
	}
	code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
	ok := false
	if len(code) == totpDigits {
		ok = s.checkTOTP(ctx, userID, code, s.now())
	} else {
		ok = s.consumeRecoveryCode(ctx, userID, code)
	}
	if !ok {
		return ErrBadCode
	}
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE users SET totp_secret_enc = NULL, totp_pending_enc = NULL, totp_last_step = 0 WHERE id = ?`, userID); err != nil {
		return err
	}
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID)
	_ = s.st.Audit(ctx, userID, "auth.totp_disabled", userID, "")
	return nil
}

// RecoveryCodesLeft counts unused recovery codes.
func (s *Service) RecoveryCodesLeft(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

func (s *Service) consumeRecoveryCode(ctx context.Context, userID, code string) bool {
	code = strings.ToLower(strings.ReplaceAll(code, "-", ""))
	if len(code) != 10 {
		return false
	}
	res, err := s.st.DB.ExecContext(ctx, `UPDATE recovery_codes SET used_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`, userID, hashToken(code))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		_ = s.st.Audit(ctx, userID, "auth.recovery_code_used", userID, "")
	}
	return n == 1
}

func newRecoveryCodes() (codes, hashes []string, err error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
	for i := 0; i < recoveryCodeCount; i++ {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		for j := range b {
			b[j] = alphabet[int(b[j])%len(alphabet)]
		}
		raw := string(b)
		codes = append(codes, raw[:5]+"-"+raw[5:])
		hashes = append(hashes, hashToken(raw))
	}
	return codes, hashes, nil
}

// ---- helpers ----

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetProjects limits a deployer or viewer to apps whose names match the
// comma-separated globs (empty = everything the role allows).
func (s *Service) SetProjects(ctx context.Context, id, projects string) error {
	var parts []string
	for _, p := range strings.Split(projects, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.ContainsAny(p, " /\\") || len(p) > 40 {
			return errors.New("project entries are app names or globs like shop-*")
		}
		parts = append(parts, p)
	}
	_, err := s.st.DB.ExecContext(ctx, `UPDATE users SET projects = ? WHERE id = ?`, strings.Join(parts, ","), id)
	return err
}
