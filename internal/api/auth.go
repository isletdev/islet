package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/pkg/api"
)

const sessionCookie = "islet_session"

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxUser
	ctxToken
	ctxLocal
)

// sessionFrom returns the verified session of the request, if any.
func sessionFrom(ctx context.Context) *auth.Session {
	s, _ := ctx.Value(ctxSession).(*auth.Session)
	return s
}

// newLoginIP records the address and reports whether it was unseen for
// this user (ignoring the very first login, which is always new).
func (s *Server) newLoginIP(ctx context.Context, username, ip string) bool {
	if ip == "" || ip == "127.0.0.1" || ip == "::1" {
		return false
	}
	key := "auth.ips." + username
	v, _, _ := s.store.Setting(ctx, key)
	seen := map[string]bool{}
	for _, x := range strings.Split(v, ",") {
		if x != "" {
			seen[x] = true
		}
	}
	if seen[ip] {
		return false
	}
	seen[ip] = true
	var list []string
	for x := range seen {
		list = append(list, x)
	}
	if len(list) > 50 {
		list = list[len(list)-50:]
	}
	_ = s.store.SetSetting(ctx, key, strings.Join(list, ","))
	return v != ""
}

// LocalConn marks a connection context as coming from the Unix socket.
func LocalConn(ctx context.Context) context.Context { return context.WithValue(ctx, ctxLocal, true) }

func userFrom(ctx context.Context) *auth.User {
	u, _ := ctx.Value(ctxUser).(*auth.User)
	return u
}

// withSession resolves the cookie on every request and stores the session and
// user in the context. It does not reject anything; requireAuth does.
func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if local, _ := r.Context().Value(ctxLocal).(bool); local {
			// A connection on the root-only Unix socket acts as the first admin.
			if users, err := s.auth.Users(r.Context()); err == nil {
				for _, u := range users {
					if u.Role == "admin" {
						uu := u
						ctx := context.WithValue(r.Context(), ctxUser, &uu)
						ctx = context.WithValue(ctx, ctxSession, &auth.Session{ID: "socket", UserID: u.ID})
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
		}
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			u, t, err := s.auth.UserByToken(r.Context(), strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, api.Error{Error: "bad_token", Message: "invalid or expired API token"})
				return
			}
			if !auth.ScopeAllows(t.Scopes, r.Method, r.URL.Path) {
				writeJSON(w, http.StatusForbidden, api.Error{Error: "scope", Message: "this token's scopes do not cover " + r.Method + " " + r.URL.Path})
				return
			}
			ctx := context.WithValue(r.Context(), ctxUser, u)
			ctx = context.WithValue(ctx, ctxToken, t)
			ctx = context.WithValue(ctx, ctxSession, &auth.Session{ID: "token:" + t.ID, UserID: u.ID})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess, err := s.auth.SessionByToken(r.Context(), c.Value)
		if err != nil {
			clearSessionCookie(w, r)
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), ctxSession, sess)
		if !sess.MFAPending {
			if u, err := s.auth.UserByID(ctx, sess.UserID); err == nil {
				ctx = context.WithValue(ctx, ctxUser, u)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAuth rejects requests without a fully verified session.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if sess == nil {
			writeJSON(w, http.StatusUnauthorized, api.Error{Error: "unauthenticated", Message: "sign in to continue"})
			return
		}
		if sess.MFAPending || userFrom(r.Context()) == nil {
			writeJSON(w, http.StatusUnauthorized, api.Error{Error: "mfa_required", Message: "second factor required"})
			return
		}
		next(w, r)
	}
}

// requireJSON blocks cross-site form posts: a browser cannot send
// application/json cross-origin without a CORS preflight, which we never grant.
func requireJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if r.ContentLength != 0 && !strings.HasPrefix(ct, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, api.Error{Error: "bad_content_type", Message: "send application/json"})
			return
		}
		if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
			writeJSON(w, http.StatusForbidden, api.Error{Error: "cross_site", Message: "cross-site request refused"})
			return
		}
		next(w, r)
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: isSecure(r),
		SameSite: http.SameSiteLaxMode, Expires: exp, Domain: currentCookieDomain(r),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1, Domain: currentCookieDomain(r)})
}

func toAPIUser(u *auth.User) api.User {
	return api.User{ID: u.ID, Username: u.Username, Role: u.Role, Projects: u.Projects, TOTPEnabled: u.TOTPEnabled, IsService: u.IsService, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

func authError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeJSON(w, http.StatusUnauthorized, api.Error{Error: "invalid_credentials", Message: err.Error()})
	case errors.Is(err, auth.ErrRateLimited):
		writeJSON(w, http.StatusTooManyRequests, api.Error{Error: "rate_limited", Message: "too many attempts, try again later"})
	case errors.Is(err, auth.ErrBadCode):
		writeJSON(w, http.StatusUnauthorized, api.Error{Error: "bad_code", Message: err.Error()})
	case errors.Is(err, auth.ErrSetupDone), errors.Is(err, auth.ErrBadSetupToken), errors.Is(err, auth.ErrTOTPAlreadyOn),
		errors.Is(err, auth.ErrTOTPOff), errors.Is(err, auth.ErrTOTPNotPending), errors.Is(err, auth.ErrWeakPassword):
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
	default:
		if msg := err.Error(); strings.HasPrefix(msg, "username") || strings.HasPrefix(msg, "password") {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: msg})
			return
		}
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: "internal error"})
	}
}

// ---- handlers ----

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	needs, err := s.auth.NeedsSetup(r.Context())
	if err != nil {
		authError(w, err)
		return
	}
	d, _ := cookieDomain.Load().(string)
	writeJSON(w, http.StatusOK, api.SetupStatus{NeedsSetup: needs, CookieDomain: d})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req api.SetupRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u, err := s.auth.CompleteSetup(r.Context(), req.Token, req.Username, req.Password)
	if err != nil {
		authError(w, err)
		return
	}
	token, sess, err := s.auth.Login(r.Context(), u.Username, req.Password, clientIP(r), r.UserAgent())
	if err != nil {
		authError(w, err)
		return
	}
	setSessionCookie(w, r, token, sess.ExpiresAt)
	au := toAPIUser(u)
	writeJSON(w, http.StatusCreated, api.LoginResponse{User: &au})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req api.LoginRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	token, sess, err := s.auth.Login(r.Context(), req.Username, req.Password, clientIP(r), r.UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			w.Header().Set("Retry-After", strconv.Itoa(int(s.auth.RetryAfter(req.Username, clientIP(r)).Seconds())+1))
			if s.notify != nil {
				s.notify.Emit(r.Context(), notify.Event{Category: "security", Severity: notify.Warning, Title: "Repeated failed logins", Message: "Too many failed panel logins for " + req.Username + " from " + clientIP(r) + ". The client is rate limited.", Link: "/settings"})
			}
		}
		authError(w, err)
		return
	}
	setSessionCookie(w, r, token, sess.ExpiresAt)
	if s.notify != nil && !sess.MFAPending {
		sev, title, msg := notify.Info, "Panel login: "+req.Username, "Signed in from "+clientIP(r)+"."
		if s.newLoginIP(r.Context(), req.Username, clientIP(r)) {
			where := clientIP(r)
			if s.geoEnabled(r.Context()) {
				if g := lookupGeo(r.Context(), clientIP(r)); g != "" {
					where += " (" + g + ")"
				}
			}
			sev, title, msg = notify.Warning, "Login from a new address: "+req.Username, "First sign-in from "+where+". If this was not you, change the password and revoke sessions in Settings."
		}
		s.notify.Emit(r.Context(), notify.Event{Category: "security", Severity: sev, Title: title, Message: msg, Link: "/settings"})
	}
	if sess.MFAPending {
		writeJSON(w, http.StatusOK, api.LoginResponse{MFARequired: true})
		return
	}
	u, err := s.auth.UserByID(r.Context(), sess.UserID)
	if err != nil {
		authError(w, err)
		return
	}
	au := toAPIUser(u)
	writeJSON(w, http.StatusOK, api.LoginResponse{User: &au})
}

func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, api.Error{Error: "unauthenticated", Message: "sign in first"})
		return
	}
	var req api.CodeRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if err := s.auth.VerifySecondFactor(r.Context(), sess, req.Code, clientIP(r)); err != nil {
		authError(w, err)
		return
	}
	u, err := s.auth.UserByID(r.Context(), sess.UserID)
	if err != nil {
		authError(w, err)
		return
	}
	au := toAPIUser(u)
	writeJSON(w, http.StatusOK, api.LoginResponse{User: &au})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	left, _ := s.auth.RecoveryCodesLeft(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, api.Me{User: toAPIUser(u), SessionID: sess.ID, RecoveryCodesLeft: left})
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	var req api.PasswordChangeRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u, sess := userFrom(r.Context()), sessionFrom(r.Context())
	if err := s.auth.ChangePassword(r.Context(), u.ID, req.Current, req.New, sess.ID); err != nil {
		authError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	secret, url, err := s.auth.BeginTOTPSetup(r.Context(), u.ID)
	if err != nil {
		authError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.TOTPSetup{Secret: secret, OTPAuthURL: url})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var req api.CodeRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u := userFrom(r.Context())
	codes, err := s.auth.EnableTOTP(r.Context(), u.ID, req.Code)
	if err != nil {
		authError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.RecoveryCodes{Codes: codes})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req api.CodeRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u := userFrom(r.Context())
	if err := s.auth.DisableTOTP(r.Context(), u.ID, req.Code); err != nil {
		authError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	u, cur := userFrom(r.Context()), sessionFrom(r.Context())
	list, err := s.auth.Sessions(r.Context(), u.ID)
	if err != nil {
		authError(w, err)
		return
	}
	out := make([]api.Session, 0, len(list))
	for _, x := range list {
		out = append(out, api.Session{ID: x.ID, Current: x.ID == cur.ID, IP: x.IP, UserAgent: x.UserAgent,
			CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt, ExpiresAt: x.ExpiresAt.Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	u, cur := userFrom(r.Context()), sessionFrom(r.Context())
	id := r.PathValue("id")
	if err := s.auth.RevokeSession(r.Context(), u.ID, id); err != nil {
		authError(w, err)
		return
	}
	if id == cur.ID {
		clearSessionCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}
