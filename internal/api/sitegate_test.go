package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/store"
)

func siteGateServer(t *testing.T) (*Server, *auth.Service) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "islet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keys, err := auth.LoadOrCreateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	as, err := auth.New(st, keys, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range [][3]string{{"d1", "client.example.com", "site:bob"}, {"d2", "other.example.com", "*,site:carol"}} {
		if _, err := st.DB.ExecContext(ctx, `INSERT INTO domains (id, server_id, host, target_type, target, port, tls, protect, protect_users, enabled) VALUES (?, ?, ?, 'url', 'http://x', 0, 'letsencrypt', 1, ?, 1)`,
			d[0], st.ServerID, d[1], d[2]); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{store: st, auth: as, proxy: proxy.New(nil, st, dir, "")}, as
}

func gateAsk(s *Server, host, uri, list string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/_islet/auth?u="+url.QueryEscape(list), nil)
	r.Header.Set("X-Forwarded-Host", host)
	r.Header.Set("X-Forwarded-Uri", uri)
	r.Header.Set("X-Forwarded-Proto", "https")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.handleForwardAuth(w, r)
	return w
}

// The whole path a colleague takes: turned away to the site's own sign-in,
// signed in there, let through with their name — and refused on a site, or a
// route, they were not given.
func TestSitePersonThroughTheGate(t *testing.T) {
	s, as := siteGateServer(t)
	ctx := context.Background()
	if _, err := as.CreateSiteUser(ctx, "admin", "bob", "Bob", "correct horse battery", false); err != nil {
		t.Fatal(err)
	}

	w := gateAsk(s, "client.example.com", "/reports?q=1", "site:bob")
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.HasPrefix(loc, "https://client.example.com/_islet/login?") || !strings.Contains(loc, "next=%2Freports%3Fq%3D1") {
		t.Fatalf("a stranger was sent to %d %q", w.Code, loc)
	}
	if strings.Contains(loc, "a=1") {
		t.Error("a route for site people only offered the panel sign-in")
	}

	// Signing in on the site's own page.
	form := url.Values{"step": {"password"}, "username": {"bob"}, "password": {"correct horse battery"}, "next": {"/reports?q=1"}}
	r := httptest.NewRequest("POST", "/_islet/login", strings.NewReader(form.Encode()))
	r.Host = "client.example.com"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://client.example.com")
	w = httptest.NewRecorder()
	s.handleSiteLogin(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/reports?q=1" {
		t.Fatalf("sign-in answered %d %q: %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == siteCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.Domain != "" || !cookie.HttpOnly {
		t.Fatalf("site cookie = %+v", cookie)
	}

	w = gateAsk(s, "client.example.com", "/reports", "site:bob", cookie)
	if w.Code != http.StatusOK || w.Header().Get("X-Islet-User") != "bob" || w.Header().Get("X-Islet-Role") != "site" {
		t.Fatalf("bob was not let in: %d %v", w.Code, w.Header())
	}
	// Another route on the same host that does not name him.
	if w = gateAsk(s, "client.example.com", "/admin", "alice", cookie); w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "Islet panel") {
		t.Fatalf("a route not naming bob answered %d, or told him about the panel", w.Code)
	}
	// The same cookie at another protected site, as an application that has
	// seen it might try.
	if w = gateAsk(s, "other.example.com", "/", "site:bob", cookie); w.Code != http.StatusFound {
		t.Fatalf("bob's cookie from one site opened another: %d", w.Code)
	}
	// An empty list is panel accounts, as it always was.
	if w = gateAsk(s, "client.example.com", "/", "", cookie); w.Code == http.StatusOK {
		t.Fatal("an empty list let a site person in")
	}
}

// The sign-in page answers only on a protected domain, says nothing it should
// not, and is served under a policy that lets it load nothing and post nowhere
// else — with the look saved for that domain.
func TestSiteLoginPage(t *testing.T) {
	s, _ := siteGateServer(t)
	ctx := context.Background()
	logo := "data:image/png;base64,iVBORw0KGgo="
	if err := s.proxy.SetLoginPage(ctx, "admin", proxy.LoginPage{DomainID: "d1", Title: "Acme client portal", Message: "Ask Jasir for access.", Logo: logo, Accent: "#2563eb", CSS: "h1{letter-spacing:.02em}"}); err != nil {
		t.Fatal(err)
	}
	get := func(host, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = host
		w := httptest.NewRecorder()
		s.handleSiteLogin(w, r)
		return w
	}
	if w := get("panel.example.com", "/_islet/login"); w.Code != http.StatusNotFound {
		t.Fatalf("a host that is not protected served the page: %d", w.Code)
	}
	w := get("client.example.com", "/_islet/login?next=https://evil.example/x")
	body := w.Body.String()
	for _, want := range []string{"Acme client portal", "Ask Jasir for access.", `src="` + logo + `"`, "--accent:#2563eb", "letter-spacing:.02em", `name="next" value="/"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(body, "ZgotmplZ") || strings.Contains(body, "Islet account") {
		t.Error("the page carries a filtered value or offers the panel to a site-people-only route")
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "form-action 'self'") {
		t.Fatalf("policy = %q", csp)
	}
	// With the Islet link off, a route that admits panel accounts still says
	// nothing about Islet.
	if err := s.proxy.SetLoginPage(ctx, "admin", proxy.LoginPage{DomainID: "d2", Title: "Portal", PanelLink: false}); err != nil {
		t.Fatal(err)
	}
	if body := get("other.example.com", "/_islet/login?a=1").Body.String(); strings.Contains(body, "Islet") {
		t.Error("a page with the Islet link turned off mentions Islet")
	}
	// Bad CSS is refused when saved, not discovered when drawn.
	if err := s.proxy.SetLoginPage(ctx, "admin", proxy.LoginPage{DomainID: "d1", CSS: "</style><script>alert(1)</script>"}); err == nil {
		t.Fatal("CSS that closes its element was saved")
	}
	// A form posted from another site is refused.
	r := httptest.NewRequest("POST", "/_islet/login", strings.NewReader("username=bob&password=x"))
	r.Host = "client.example.com"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	s.handleSiteLogin(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a cross-site sign-in answered %d", w.Code)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"/a?b=1": "/a?b=1", "//evil.example": "/", "https://evil.example": "/", "/\\evil": "/", "": "/", "/_islet/logout": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
