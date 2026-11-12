package api

import (
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/proxy"
)

// siteCookie is a site person's sign-in on one protected host. Host-only: it
// is set by the sign-in page that host serves, and no other name receives it.
const siteCookie = "islet_site"

// The sign-in a protected site serves at /_islet/login, /_islet/logout and
// /_islet/invite. The proxy routes those three paths on every host that asks
// for a login to this daemon, with the visitor's own Host, so everything here
// is about the host in the request — and refuses any host that is not a
// protected domain, which is what keeps the pages from answering on the
// panel's address or a name pointed here by somebody else.

func (s *Server) siteDomain(w http.ResponseWriter, r *http.Request) (*proxy.Domain, bool) {
	if s.proxy == nil {
		http.NotFound(w, r)
		return nil, false
	}
	d := s.proxy.ProtectedDomainFor(r.Context(), auth.NormHost(r.Host))
	if d == nil {
		http.NotFound(w, r)
		return nil, false
	}
	return d, true
}

// sameOrigin refuses a form posted from another site. The cookie is Lax, so
// this is about a sign-in being started from somewhere else — logging a
// visitor in as the attacker — rather than about stealing one.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" || o == "null" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// siteClientIP is the visitor's address. These pages are reached through the
// proxy, so the connection comes from the proxy's container; the address it
// recorded is believed only from there, and from nowhere else.
func siteClientIP(r *http.Request) string {
	ip := clientIP(r)
	if p := net.ParseIP(ip); p != nil && (p.IsPrivate() || p.IsLoopback()) {
		if real := strings.TrimSpace(r.Header.Get("X-Real-Ip")); net.ParseIP(real) != nil {
			return real
		}
	}
	return ip
}

// safeNext keeps a return address on this host: a path, never another site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.HasPrefix(next, "/_islet/") {
		return "/"
	}
	return next
}

func setSiteCookie(w http.ResponseWriter, r *http.Request, tok string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{Name: siteCookie, Value: tok, Path: "/", HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode, Expires: exp})
}

func clearSiteCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: siteCookie, Value: "", Path: "/", HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// sitePage is what the template draws.
type sitePage struct {
	Look     proxy.LoginPage
	Host     string
	Step     string // password, code, enrol, invite, done, signedout
	Next     string
	Error    string
	Notice   string
	Username string
	Secret   string
	OTPAuth  template.URL
	Panel    string
	Token    string
	// The look's values in the types the template trusts. Each was checked
	// when it was saved — the logo is a base64 image, the accent a hex colour,
	// the CSS cannot close its element — and html/template would otherwise
	// replace a data: URL and any CSS with a placeholder.
	Logo   template.URL
	Accent template.CSS
	CSS    template.CSS
}

func (s *Server) renderSite(w http.ResponseWriter, r *http.Request, d *proxy.Domain, p sitePage, status int) {
	look, _ := s.proxy.LoginPage(r.Context(), d.ID)
	p.Look, p.Host = look, auth.NormHost(r.Host)
	if !look.PanelLink {
		p.Panel = ""
	}
	p.Logo, p.Accent, p.CSS = template.URL(look.Logo), template.CSS(look.Accent), template.CSS(look.CSS)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	// Nothing loads from anywhere: the logo is inline, the CSS is inline, and
	// the form posts only here. Custom CSS can restyle the page and cannot
	// send what is typed into it anywhere.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = siteTmpl.Execute(w, p)
}

// handleSiteLogin is the sign-in page and its three steps: password, then a
// code for somebody with a second factor, or setting one up for somebody who
// must have one and does not yet.
func (s *Server) handleSiteLogin(w http.ResponseWriter, r *http.Request) {
	d, ok := s.siteDomain(w, r)
	if !ok {
		return
	}
	host := auth.NormHost(r.Host)
	next := safeNext(r.FormValue("next"))
	p := sitePage{Step: "password", Next: next}
	if r.FormValue("a") == "1" {
		if panel := s.panelURL(r.Context()); panel != "" {
			p.Panel = panel + "/login?next=" + url.QueryEscape(proto(r)+"://"+host+next)
		}
	}
	if r.FormValue("set") == "1" {
		p.Notice = "Your password is set. Sign in with it."
	}

	// Somebody part-way through: their password was right and a code is owed.
	var sess *auth.SiteSession
	var person *auth.SiteUser
	if c, err := r.Cookie(siteCookie); err == nil && c.Value != "" {
		sess, person, _ = s.auth.SiteSessionByToken(r.Context(), c.Value, host)
	}
	if sess != nil && !sess.MFAPending && r.Method == http.MethodGet {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	if sess != nil && sess.MFAPending {
		p.Username = person.Username
		p.Step = "code"
		if !person.TOTPEnabled {
			p.Step = "enrol"
		}
	}

	if r.Method != http.MethodPost {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.fillEnrol(r, &p, person)
		s.renderSite(w, r, d, p, http.StatusOK)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "this form was sent from another site", http.StatusForbidden)
		return
	}
	ip := siteClientIP(r)
	switch r.FormValue("step") {
	case "code", "enrol":
		if sess == nil || !sess.MFAPending {
			p.Step, p.Error = "password", "Your sign-in expired. Start again."
			break
		}
		var err error
		if p.Step == "enrol" {
			err = s.auth.SiteEnrolTOTP(r.Context(), sess, person, r.FormValue("code"), ip)
		} else {
			err = s.auth.SiteSecondFactor(r.Context(), sess, person, r.FormValue("code"), ip)
		}
		if err == nil {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		p.Error = "That code is not right. Use the one your authenticator shows now."
		if errors.Is(err, auth.ErrRateLimited) {
			p.Error = "Too many attempts. Wait a few minutes and try again."
		}
	default:
		// A fresh password ends whatever was half-done on this browser.
		if c, err := r.Cookie(siteCookie); err == nil {
			s.auth.SiteLogout(r.Context(), c.Value)
		}
		tok, ns, err := s.auth.SiteLogin(r.Context(), r.FormValue("username"), r.FormValue("password"), host, ip, r.UserAgent())
		if err != nil {
			p.Step = "password"
			p.Username = auth.NormSiteUsername(r.FormValue("username"))
			p.Error = "That username and password do not match."
			if errors.Is(err, auth.ErrRateLimited) {
				p.Error = "Too many attempts. Wait a few minutes and try again."
			}
			clearSiteCookie(w, r)
			s.renderSite(w, r, d, p, http.StatusUnauthorized)
			return
		}
		setSiteCookie(w, r, tok, ns.ExpiresAt)
		if !ns.MFAPending {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		// The code step is a page of its own, so a reload does not re-send the
		// password.
		http.Redirect(w, r, "/_islet/login?"+url.Values{"next": {next}}.Encode(), http.StatusSeeOther)
		return
	}
	s.fillEnrol(r, &p, person)
	s.renderSite(w, r, d, p, http.StatusUnauthorized)
}

func (s *Server) fillEnrol(r *http.Request, p *sitePage, person *auth.SiteUser) {
	if p.Step != "enrol" || person == nil {
		return
	}
	secret, otp, err := s.auth.SiteBeginTOTP(r.Context(), person)
	if err != nil {
		p.Step, p.Error = "password", "Two-factor setup could not start. Sign in again."
		return
	}
	// Grouped in fours, which is how people type a key off a screen.
	var b strings.Builder
	for i, c := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	p.Secret, p.OTPAuth = b.String(), template.URL(otp)
}

func proto(r *http.Request) string {
	if isSecure(r) {
		return "https"
	}
	return "http"
}

func (s *Server) handleSiteLogout(w http.ResponseWriter, r *http.Request) {
	d, ok := s.siteDomain(w, r)
	if !ok {
		return
	}
	// Signing out from a link elsewhere is a nuisance at worst, but a GET
	// that changes state is still a GET a prefetcher can make: the page asks
	// for a click.
	if r.Method == http.MethodPost && sameOrigin(r) {
		if c, err := r.Cookie(siteCookie); err == nil {
			s.auth.SiteLogout(r.Context(), c.Value)
		}
		clearSiteCookie(w, r)
		s.renderSite(w, r, d, sitePage{Step: "signedout", Next: "/"}, http.StatusOK)
		return
	}
	s.renderSite(w, r, d, sitePage{Step: "logout", Next: "/"}, http.StatusOK)
}

// handleSiteInvite is where an invitation link lands: set a password, then
// sign in with it.
func (s *Server) handleSiteInvite(w http.ResponseWriter, r *http.Request) {
	d, ok := s.siteDomain(w, r)
	if !ok {
		return
	}
	tok := r.FormValue("t")
	p := sitePage{Step: "invite", Token: tok, Next: "/"}
	person, err := s.auth.SiteInviteUser(r.Context(), tok)
	if err != nil {
		p.Step, p.Error = "done", auth.ErrBadInvite.Error()
		s.renderSite(w, r, d, p, http.StatusGone)
		return
	}
	p.Username = person.Username
	if r.Method != http.MethodPost {
		s.renderSite(w, r, d, p, http.StatusOK)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "this form was sent from another site", http.StatusForbidden)
		return
	}
	pw := r.FormValue("password")
	if pw != r.FormValue("confirm") {
		p.Error = "The two passwords are not the same."
		s.renderSite(w, r, d, p, http.StatusBadRequest)
		return
	}
	if _, err := s.auth.AcceptSiteInvite(r.Context(), tok, pw); err != nil {
		p.Error = err.Error()
		if errors.Is(err, auth.ErrWeakPassword) {
			p.Error = "Use at least 12 characters."
		}
		s.renderSite(w, r, d, p, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/_islet/login?set=1", http.StatusSeeOther)
}

// denySite answers a site person who is signed in on this host but not named
// on this route. Unlike the panel's refusal it says nothing about Islet or
// where the panel is: this is somebody outside.
func (s *Server) denySite(w http.ResponseWriter, r *http.Request, who, site string) {
	if firstDenialIn(time.Minute, auth.SitePrefix+who+"|"+site+r.Header.Get("X-Forwarded-Uri")) {
		_ = s.store.Audit(r.Context(), auth.SitePrefix+who, "site.denied", site+r.Header.Get("X-Forwarded-Uri"), "")
	}
	d := s.proxy.ProtectedDomainFor(r.Context(), auth.NormHost(site))
	if d == nil {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	r2 := r.Clone(r.Context())
	r2.Host = site
	s.renderSite(w, r2, d, sitePage{Step: "denied", Username: who, Next: "/"}, http.StatusForbidden)
}

var siteTmpl = template.Must(template.New("site").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex">
<title>{{if .Look.Title}}{{.Look.Title}}{{else}}Sign in{{end}}</title>
<style>
:root{--bg:#fafafa;--card:#fff;--ink:#0a0a0a;--muted:#525252;--line:#e5e5e5;--accent:{{if .Accent}}{{.Accent}}{{else}}#0a0a0a{{end}};--on-accent:#fff;--danger:#b91c1c;--ok:#15803d}
{{if eq .Look.Theme "dark"}}:root{--bg:#0a0a0a;--card:#171717;--ink:#fafafa;--muted:#a3a3a3;--line:#262626;{{if not .Look.Accent}}--accent:#fafafa;--on-accent:#0a0a0a;{{end}}--danger:#f87171;--ok:#4ade80}
{{else if eq .Look.Theme "auto"}}@media (prefers-color-scheme:dark){:root{--bg:#0a0a0a;--card:#171717;--ink:#fafafa;--muted:#a3a3a3;--line:#262626;{{if not .Look.Accent}}--accent:#fafafa;--on-accent:#0a0a0a;{{end}}--danger:#f87171;--ok:#4ade80}}{{end}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;padding:16px;background:var(--bg);color:var(--ink);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{width:100%;max-width:400px;background:var(--card);border:1px solid var(--line);border-radius:12px;padding:32px 28px}
.logo{display:block;max-width:180px;max-height:64px;margin:0 0 20px}
h1{font-size:22px;line-height:1.3;margin:0 0 6px}
.msg{color:var(--muted);margin:0 0 20px;white-space:pre-line}
label{display:block;font-size:14px;font-weight:600;margin:14px 0 6px}
input{width:100%;font:inherit;padding:10px 12px;border:1px solid var(--line);border-radius:8px;background:var(--bg);color:var(--ink)}
input:focus{outline:2px solid var(--accent);outline-offset:1px}
button{width:100%;margin-top:20px;font:inherit;font-weight:600;padding:11px 14px;border:0;border-radius:8px;background:var(--accent);color:var(--on-accent);cursor:pointer}
.err{color:var(--danger);font-size:14px;margin:12px 0 0}
.ok{color:var(--ok);font-size:14px;margin:0 0 8px}
.key{font:15px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:.04em;padding:10px 12px;border:1px dashed var(--line);border-radius:8px;word-break:break-all;margin:8px 0}
.alt{margin:18px 0 0;font-size:14px;color:var(--muted);text-align:center}
a{color:inherit}
small{color:var(--muted);font-size:13px}
</style>
{{if .CSS}}<style>{{.CSS}}</style>{{end}}
</head><body><main>
{{if .Logo}}<img class="logo" src="{{.Logo}}" alt="">{{end}}
{{if eq .Step "password"}}
<h1>{{if .Look.Title}}{{.Look.Title}}{{else}}Sign in{{end}}</h1>
<p class="msg">{{if .Look.Message}}{{.Look.Message}}{{else}}Sign in to continue to {{.Host}}.{{end}}</p>
{{if .Notice}}<p class="ok">{{.Notice}}</p>{{end}}
<form method="post" action="/_islet/login">
<input type="hidden" name="step" value="password"><input type="hidden" name="next" value="{{.Next}}">
<label for="u">Username</label><input id="u" name="username" value="{{.Username}}" autocomplete="username" autocapitalize="none" spellcheck="false" required {{if not .Username}}autofocus{{end}}>
<label for="p">Password</label><input id="p" name="password" type="password" autocomplete="current-password" required {{if .Username}}autofocus{{end}}>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
<button type="submit">Sign in</button>
</form>
{{if .Panel}}<p class="alt"><a href="{{.Panel}}">Sign in with an Islet account instead</a></p>{{end}}
{{else if eq .Step "code"}}
<h1>Enter your code</h1>
<p class="msg">Signed in as {{.Username}}. Enter the six-digit code from your authenticator app.</p>
<form method="post" action="/_islet/login">
<input type="hidden" name="step" value="code"><input type="hidden" name="next" value="{{.Next}}">
<label for="c">Code</label><input id="c" name="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9 ]{6,7}" maxlength="7" required autofocus>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
<button type="submit">Continue</button>
</form>
{{else if eq .Step "enrol"}}
<h1>Set up two-factor sign-in</h1>
<p class="msg">This site asks {{.Username}} for a code from an authenticator app as well as a password. Add this key to your app (Google Authenticator, 1Password, Authy…), then enter the code it shows.</p>
<div class="key">{{.Secret}}</div>
<p><small>On a phone with an authenticator app: <a href="{{.OTPAuth}}">add it directly</a>.</small></p>
<form method="post" action="/_islet/login">
<input type="hidden" name="step" value="enrol"><input type="hidden" name="next" value="{{.Next}}">
<label for="c">Code</label><input id="c" name="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9 ]{6,7}" maxlength="7" required autofocus>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
<button type="submit">Turn on and continue</button>
</form>
{{else if eq .Step "invite"}}
<h1>Choose a password</h1>
<p class="msg">For {{.Username}} on {{.Host}}. At least 12 characters.</p>
<form method="post" action="/_islet/invite">
<input type="hidden" name="t" value="{{.Token}}">
<input type="text" name="username" value="{{.Username}}" autocomplete="username" hidden>
<label for="p">Password</label><input id="p" name="password" type="password" autocomplete="new-password" minlength="12" required autofocus>
<label for="p2">The same again</label><input id="p2" name="confirm" type="password" autocomplete="new-password" minlength="12" required>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
<button type="submit">Set password</button>
</form>
{{else if eq .Step "denied"}}
<h1>No access</h1>
<p class="msg">You are signed in as {{.Username}}, and this part of {{.Host}} is not open to you. Ask whoever gave you access to add it.</p>
<form method="post" action="/_islet/logout"><button type="submit">Sign out</button></form>
{{else if eq .Step "logout"}}
<h1>Sign out of {{.Host}}?</h1>
<form method="post" action="/_islet/logout"><button type="submit">Sign out</button></form>
{{else if eq .Step "signedout"}}
<h1>Signed out</h1>
<p class="msg">You are signed out of {{.Host}}.</p>
<p class="alt"><a href="/_islet/login">Sign in again</a></p>
{{else}}
<h1>{{if .Look.Title}}{{.Look.Title}}{{else}}Sign in{{end}}</h1>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
<p class="alt"><a href="/_islet/login">Go to sign in</a></p>
{{end}}
</main></body></html>`))
