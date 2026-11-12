package auth

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Site people sign in to protected sites and to nothing else.
//
// They live in their own tables, with their own sessions, and nothing here
// hands one of them a panel session, a gate token or an API token. The panel's
// checks read users and sessions only, so the separation does not depend on
// every one of those checks remembering to refuse somebody.

// SitePrefix marks a site person in a protection allow-list, beside panel
// usernames: "alice,site:bob" admits the panel account alice and the site
// person bob. The prefix is what keeps the two namespaces apart — a panel
// account and a site person may share a name and still be two people.
const SitePrefix = "site:"

// InviteTTL is how long an invitation link works.
const InviteTTL = 72 * time.Hour

var (
	// ErrSiteNoAccess is a site person signing in who is disabled or has no
	// password yet. Reported to them as ordinary bad credentials.
	ErrSiteNoAccess = errors.New("this person cannot sign in")
	// ErrBadInvite is an invitation that is unknown, used or expired.
	ErrBadInvite = errors.New("this invitation link is not valid any more; ask for a new one")

	// Wider than a panel username: an email address is the natural name for
	// somebody outside, and it is only ever a label here.
	siteUsernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.@+-]{2,63}$`)
)

// SiteUser is a person who may be let into protected sites.
type SiteUser struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Name          string `json:"name"`
	HasPassword   bool   `json:"hasPassword"`
	TOTPEnabled   bool   `json:"totpEnabled"`
	RequireTOTP   bool   `json:"requireTotp"`
	Disabled      bool   `json:"disabled"`
	InviteExpires string `json:"inviteExpires,omitempty"`
	CreatedAt     string `json:"createdAt"`
	CreatedBy     string `json:"createdBy"`
	LastLoginAt   string `json:"lastLoginAt"`
}

// SiteSession is a site person's sign-in on one host.
type SiteSession struct {
	ID         string
	UserID     string
	Host       string
	MFAPending bool
	ExpiresAt  time.Time
}

// SiteUserPatch changes what is set and leaves the rest.
type SiteUserPatch struct {
	Name        *string `json:"name"`
	Disabled    *bool   `json:"disabled"`
	RequireTOTP *bool   `json:"requireTotp"`
	Password    *string `json:"password"`
	ResetTOTP   bool    `json:"resetTotp"`
}

const siteUserCols = `id, username, name, password_hash != '', totp_secret_enc IS NOT NULL AND length(totp_secret_enc) > 0, require_totp, disabled, invite_hash, invite_expires, created_at, created_by, last_login_at`

func scanSiteUser(sc interface{ Scan(...any) error }) (*SiteUser, error) {
	var u SiteUser
	var pw, totp, req, dis int
	var invHash string
	if err := sc.Scan(&u.ID, &u.Username, &u.Name, &pw, &totp, &req, &dis, &invHash, &u.InviteExpires, &u.CreatedAt, &u.CreatedBy, &u.LastLoginAt); err != nil {
		return nil, err
	}
	u.HasPassword, u.TOTPEnabled, u.RequireTOTP, u.Disabled = pw == 1, totp == 1, req == 1, dis == 1
	if invHash == "" {
		u.InviteExpires = ""
	}
	return &u, nil
}

// NormSiteUsername is how a site person's name is stored and compared.
func NormSiteUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// SiteUsers lists everybody, newest first.
func (s *Service) SiteUsers(ctx context.Context) ([]SiteUser, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+siteUserCols+` FROM site_users WHERE server_id = ? ORDER BY created_at DESC`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SiteUser{}
	for rows.Next() {
		u, err := scanSiteUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// SiteUser reads one person by id.
func (s *Service) SiteUser(ctx context.Context, id string) (*SiteUser, error) {
	u, err := scanSiteUser(s.st.DB.QueryRowContext(ctx, `SELECT `+siteUserCols+` FROM site_users WHERE id = ? AND server_id = ?`, id, s.st.ServerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSession
	}
	return u, err
}

// CreateSiteUser adds a person. The password may be empty, in which case they
// cannot sign in until one is set or an invitation is accepted.
func (s *Service) CreateSiteUser(ctx context.Context, actor, username, name, password string, requireTOTP bool) (*SiteUser, error) {
	username = NormSiteUsername(username)
	if !siteUsernameRe.MatchString(username) {
		return nil, errors.New("username must be 3 to 64 characters: lowercase letters, digits, and . _ - + @ (an email address works)")
	}
	hash := ""
	if password != "" {
		if err := ValidatePassword(password); err != nil {
			return nil, err
		}
		var err error
		if hash, err = HashPassword(password); err != nil {
			return nil, err
		}
	}
	id, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	_, err = s.st.DB.ExecContext(ctx, `INSERT INTO site_users (id, server_id, username, name, password_hash, require_totp, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, s.st.ServerID, username, strings.TrimSpace(name), hash, boolInt(requireTOTP), actor)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("somebody called " + username + " already exists")
		}
		return nil, err
	}
	_ = s.st.Audit(ctx, actor, "site_user.create", username, "")
	return s.SiteUser(ctx, id)
}

// UpdateSiteUser applies a patch. Disabling, a new password and a reset second
// factor all end the person's sessions, since each is done because something
// about who holds them changed.
func (s *Service) UpdateSiteUser(ctx context.Context, actor, id string, p SiteUserPatch) (*SiteUser, error) {
	u, err := s.SiteUser(ctx, id)
	if err != nil {
		return nil, err
	}
	endSessions := false
	if p.Name != nil {
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET name = ? WHERE id = ?`, strings.TrimSpace(*p.Name), id); err != nil {
			return nil, err
		}
	}
	if p.RequireTOTP != nil {
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET require_totp = ? WHERE id = ?`, boolInt(*p.RequireTOTP), id); err != nil {
			return nil, err
		}
	}
	if p.Disabled != nil {
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET disabled = ? WHERE id = ?`, boolInt(*p.Disabled), id); err != nil {
			return nil, err
		}
		endSessions = endSessions || *p.Disabled
		_ = s.st.Audit(ctx, actor, map[bool]string{true: "site_user.disable", false: "site_user.enable"}[*p.Disabled], u.Username, "")
	}
	if p.Password != nil {
		if err := ValidatePassword(*p.Password); err != nil {
			return nil, err
		}
		hash, err := HashPassword(*p.Password)
		if err != nil {
			return nil, err
		}
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET password_hash = ?, invite_hash = '', invite_expires = '' WHERE id = ?`, hash, id); err != nil {
			return nil, err
		}
		endSessions = true
		_ = s.st.Audit(ctx, actor, "site_user.password", u.Username, "")
	}
	if p.ResetTOTP {
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET totp_secret_enc = NULL, totp_pending_enc = NULL, totp_last_step = 0 WHERE id = ?`, id); err != nil {
			return nil, err
		}
		endSessions = true
		_ = s.st.Audit(ctx, actor, "site_user.totp_reset", u.Username, "")
	}
	if endSessions {
		_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM site_sessions WHERE site_user_id = ?`, id)
	}
	return s.SiteUser(ctx, id)
}

// DeleteSiteUser removes a person and, through the foreign key, every session
// they hold. It returns their username so the caller can take them off the
// allow-lists that name them.
func (s *Service) DeleteSiteUser(ctx context.Context, actor, id string) (string, error) {
	u, err := s.SiteUser(ctx, id)
	if err != nil {
		return "", err
	}
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM site_sessions WHERE site_user_id = ?`, id)
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM site_users WHERE id = ? AND server_id = ?`, id, s.st.ServerID); err != nil {
		return "", err
	}
	_ = s.st.Audit(ctx, actor, "site_user.delete", u.Username, "")
	return u.Username, nil
}

// SiteInvite issues a one-time link token for the person to set their own
// password. A new invitation replaces any earlier one.
func (s *Service) SiteInvite(ctx context.Context, actor, id string) (string, time.Time, error) {
	u, err := s.SiteUser(ctx, id)
	if err != nil {
		return "", time.Time{}, err
	}
	tok, err := randomHex(24)
	if err != nil {
		return "", time.Time{}, err
	}
	exp := s.now().Add(InviteTTL).UTC()
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET invite_hash = ?, invite_expires = ? WHERE id = ?`, hashToken(tok), exp.Format(time.RFC3339), id); err != nil {
		return "", time.Time{}, err
	}
	_ = s.st.Audit(ctx, actor, "site_user.invite", u.Username, "")
	return tok, exp, nil
}

// SiteInviteUser is who an invitation is for, or ErrBadInvite.
func (s *Service) SiteInviteUser(ctx context.Context, token string) (*SiteUser, error) {
	if token == "" {
		return nil, ErrBadInvite
	}
	var id, exp string
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, invite_expires FROM site_users WHERE invite_hash = ? AND server_id = ?`, hashToken(token), s.st.ServerID).Scan(&id, &exp)
	if err != nil {
		return nil, ErrBadInvite
	}
	if t, err := time.Parse(time.RFC3339, exp); err != nil || !s.now().Before(t) {
		return nil, ErrBadInvite
	}
	u, err := s.SiteUser(ctx, id)
	if err != nil || u.Disabled {
		return nil, ErrBadInvite
	}
	return u, nil
}

// AcceptSiteInvite sets the password an invitation was for and uses it up.
func (s *Service) AcceptSiteInvite(ctx context.Context, token, password string) (*SiteUser, error) {
	u, err := s.SiteInviteUser(ctx, token)
	if err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	res, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET password_hash = ?, invite_hash = '', invite_expires = '' WHERE id = ? AND invite_hash = ?`, hash, u.ID, hashToken(token))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrBadInvite
	}
	_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM site_sessions WHERE site_user_id = ?`, u.ID)
	_ = s.st.Audit(ctx, u.Username, "site_user.invite_accepted", u.Username, "")
	return u, nil
}

// SiteLogin checks a site person's password and opens a session on one host.
// The session is MFA-pending when they have a second factor, or when one is
// required and they have not enrolled yet; SiteSecondFactor or SiteEnrolTOTP
// completes it.
func (s *Service) SiteLogin(ctx context.Context, username, password, host, ip, ua string) (string, *SiteSession, error) {
	now := s.now()
	username = NormSiteUsername(username)
	ipKey, userKey := "site-ip:"+ip, "site-user:"+username
	if !s.loginRate.Allow(ipKey, now) || !s.loginRate.Allow(userKey, now) {
		return "", nil, ErrRateLimited
	}
	fail := func(detail string) (string, *SiteSession, error) {
		s.loginRate.Fail(ipKey, now)
		s.loginRate.Fail(userKey, now)
		_ = s.st.Audit(ctx, SitePrefix+username, "site.login_failed", host, detail+" ip="+ip)
		return "", nil, ErrInvalidCredentials
	}
	var id, hash string
	var totp []byte
	var req, dis int
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, password_hash, totp_secret_enc, require_totp, disabled FROM site_users WHERE username = ? AND server_id = ?`, username, s.st.ServerID).
		Scan(&id, &hash, &totp, &req, &dis)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && hash == "") {
		// The same time as a real check, so a name cannot be confirmed by how
		// quickly it is refused.
		VerifyPassword("$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		return fail("unknown")
	}
	if err != nil {
		return "", nil, err
	}
	if !VerifyPassword(hash, password) {
		return fail("password")
	}
	if dis == 1 {
		return fail("disabled")
	}
	s.loginRate.Reset(ipKey)
	s.loginRate.Reset(userKey)
	pending := len(totp) > 0 || req == 1
	tok, sess, err := s.createSiteSession(ctx, id, host, pending, ip, ua)
	if err != nil {
		return "", nil, err
	}
	if !pending {
		s.markSiteLogin(ctx, id, username, host, ip)
	}
	return tok, sess, nil
}

func (s *Service) markSiteLogin(ctx context.Context, id, username, host, ip string) {
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE site_users SET last_login_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, id)
	_ = s.st.Audit(ctx, SitePrefix+username, "site.login", host, "ip="+ip)
}

func (s *Service) createSiteSession(ctx context.Context, userID, host string, pending bool, ip, ua string) (string, *SiteSession, error) {
	tok, err := randomHex(32)
	if err != nil {
		return "", nil, err
	}
	id, err := randomHex(8)
	if err != nil {
		return "", nil, err
	}
	if len(ua) > 256 {
		ua = ua[:256]
	}
	exp := s.now().Add(SessionTTL).UTC()
	host = NormHost(host)
	if _, err := s.st.DB.ExecContext(ctx, `INSERT INTO site_sessions (id, server_id, token_hash, site_user_id, host, mfa_pending, ip, user_agent, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, s.st.ServerID, hashToken(tok), userID, host, boolInt(pending), ip, ua, exp.Format(time.RFC3339)); err != nil {
		return "", nil, err
	}
	return tok, &SiteSession{ID: id, UserID: userID, Host: host, MFAPending: pending, ExpiresAt: exp}, nil
}

// NormHost is a host as sessions store and compare it: lower case, no port.
func NormHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h, "]") {
		h = h[:i]
	}
	return strings.TrimSuffix(h, ".")
}

// SiteSessionByToken resolves a site cookie on a host. A session made on
// another host, an expired one and one whose person has been disabled are all
// no session.
func (s *Service) SiteSessionByToken(ctx context.Context, token, host string) (*SiteSession, *SiteUser, error) {
	if token == "" {
		return nil, nil, ErrNoSession
	}
	var sess SiteSession
	var exp string
	var pending int
	err := s.st.DB.QueryRowContext(ctx, `SELECT id, site_user_id, host, mfa_pending, expires_at FROM site_sessions WHERE token_hash = ? AND server_id = ?`, hashToken(token), s.st.ServerID).
		Scan(&sess.ID, &sess.UserID, &sess.Host, &pending, &exp)
	if err != nil {
		return nil, nil, ErrNoSession
	}
	sess.MFAPending = pending == 1
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || !s.now().Before(t) {
		_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM site_sessions WHERE id = ?`, sess.ID)
		return nil, nil, ErrNoSession
	}
	sess.ExpiresAt = t
	if sess.Host != NormHost(host) {
		return nil, nil, ErrNoSession
	}
	u, err := s.SiteUser(ctx, sess.UserID)
	if err != nil || u.Disabled {
		return nil, nil, ErrNoSession
	}
	return &sess, u, nil
}

// SiteLogout ends the session a cookie belongs to.
func (s *Service) SiteLogout(ctx context.Context, token string) {
	if token != "" {
		_, _ = s.st.DB.ExecContext(ctx, `DELETE FROM site_sessions WHERE token_hash = ?`, hashToken(token))
	}
}

// SiteSecondFactor completes a pending session with a code from the person's
// authenticator.
func (s *Service) SiteSecondFactor(ctx context.Context, sess *SiteSession, u *SiteUser, code, ip string) error {
	now := s.now()
	key := "site-user:" + u.Username
	if !s.loginRate.Allow(key, now) {
		return ErrRateLimited
	}
	var enc []byte
	var last int64
	if err := s.st.DB.QueryRowContext(ctx, `SELECT totp_secret_enc, totp_last_step FROM site_users WHERE id = ?`, u.ID).Scan(&enc, &last); err != nil || len(enc) == 0 {
		return ErrTOTPOff
	}
	secret, err := s.keys.Decrypt(enc)
	if err != nil {
		return err
	}
	step, ok := VerifyTOTP(string(secret), strings.ReplaceAll(code, " ", ""), now, last)
	if !ok {
		s.loginRate.Fail(key, now)
		_ = s.st.Audit(ctx, SitePrefix+u.Username, "site.login_failed", sess.Host, "code ip="+ip)
		return ErrBadCode
	}
	s.loginRate.Reset(key)
	_, _ = s.st.DB.ExecContext(ctx, `UPDATE site_users SET totp_last_step = ? WHERE id = ?`, step, u.ID)
	return s.completeSiteSession(ctx, sess, u, ip)
}

func (s *Service) completeSiteSession(ctx context.Context, sess *SiteSession, u *SiteUser, ip string) error {
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_sessions SET mfa_pending = 0 WHERE id = ?`, sess.ID); err != nil {
		return err
	}
	sess.MFAPending = false
	s.markSiteLogin(ctx, u.ID, u.Username, sess.Host, ip)
	return nil
}

// SiteBeginTOTP gives a person who must have a second factor and has none a
// secret to add to their authenticator. Asking again keeps the same secret, so
// a reload of the page does not invalidate what they already scanned.
func (s *Service) SiteBeginTOTP(ctx context.Context, u *SiteUser) (secret, otpauth string, err error) {
	if u.TOTPEnabled {
		return "", "", ErrTOTPAlreadyOn
	}
	var pending []byte
	_ = s.st.DB.QueryRowContext(ctx, `SELECT totp_pending_enc FROM site_users WHERE id = ?`, u.ID).Scan(&pending)
	if len(pending) > 0 {
		if b, err := s.keys.Decrypt(pending); err == nil {
			secret = string(b)
		}
	}
	if secret == "" {
		if secret, err = NewTOTPSecret(); err != nil {
			return "", "", err
		}
		enc, err := s.keys.Encrypt([]byte(secret))
		if err != nil {
			return "", "", err
		}
		if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET totp_pending_enc = ? WHERE id = ?`, enc, u.ID); err != nil {
			return "", "", err
		}
	}
	return secret, OTPAuthURL(secret, u.Username, Issuer), nil
}

// SiteEnrolTOTP confirms the pending secret with a code, turns the second
// factor on and completes the session it was asked from.
func (s *Service) SiteEnrolTOTP(ctx context.Context, sess *SiteSession, u *SiteUser, code, ip string) error {
	now := s.now()
	key := "site-user:" + u.Username
	if !s.loginRate.Allow(key, now) {
		return ErrRateLimited
	}
	var pending []byte
	if err := s.st.DB.QueryRowContext(ctx, `SELECT totp_pending_enc FROM site_users WHERE id = ?`, u.ID).Scan(&pending); err != nil || len(pending) == 0 {
		return ErrTOTPNotPending
	}
	secret, err := s.keys.Decrypt(pending)
	if err != nil {
		return err
	}
	step, ok := VerifyTOTP(string(secret), strings.ReplaceAll(code, " ", ""), now, 0)
	if !ok {
		s.loginRate.Fail(key, now)
		return ErrBadCode
	}
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE site_users SET totp_secret_enc = totp_pending_enc, totp_pending_enc = NULL, totp_last_step = ? WHERE id = ?`, step, u.ID); err != nil {
		return err
	}
	u.TOTPEnabled = true
	_ = s.st.Audit(ctx, SitePrefix+u.Username, "site.totp_enabled", sess.Host, "")
	return s.completeSiteSession(ctx, sess, u, ip)
}
