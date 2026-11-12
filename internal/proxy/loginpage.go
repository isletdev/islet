package proxy

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

// LoginPage is how a protected domain's sign-in page looks.
//
// Fields rather than an uploaded page. The page holds a password form, and a
// page somebody wrote could post it elsewhere or drop the parts the sign-in
// needs; a title, a message, a logo, a colour and some CSS cover how a client
// portal is expected to look, and the page that carries them is served under a
// policy that lets it load nothing from anywhere.
type LoginPage struct {
	DomainID string `json:"domainId"`
	Title    string `json:"title"`
	Message  string `json:"message"`
	Logo     string `json:"logo"`   // data:image/…;base64,…
	Accent   string `json:"accent"` // #rrggbb, empty for the default
	Theme    string `json:"theme"`  // auto, light or dark
	CSS      string `json:"css"`
	// PanelLink offers "sign in with an Islet account" on routes that admit
	// panel accounts. Off, the page never mentions Islet.
	PanelLink bool `json:"panelLink"`
}

const (
	maxLogoBytes = 300 << 10
	maxCSSBytes  = 20 << 10
)

var (
	accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	logoRe   = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp|svg\+xml);base64,[A-Za-z0-9+/=]+$`)
)

// Validate normalises a page and says what is wrong with it.
func (p *LoginPage) Validate() error {
	p.Title = strings.TrimSpace(p.Title)
	p.Message = strings.TrimSpace(p.Message)
	p.Accent = strings.TrimSpace(p.Accent)
	if len(p.Title) > 120 {
		return errors.New("the title is longer than 120 characters")
	}
	if len(p.Message) > 2000 {
		return errors.New("the message is longer than 2000 characters")
	}
	if p.Accent != "" && !accentRe.MatchString(p.Accent) {
		return errors.New("the accent colour must look like #2563eb")
	}
	switch p.Theme {
	case "":
		p.Theme = "auto"
	case "auto", "light", "dark":
	default:
		return errors.New("the theme must be auto, light or dark")
	}
	if p.Logo != "" {
		if len(p.Logo) > maxLogoBytes*4/3+64 {
			return errors.New("the logo is larger than 300 KB")
		}
		if !logoRe.MatchString(p.Logo) {
			return errors.New("the logo must be a PNG, JPEG, GIF, WebP or SVG image")
		}
	}
	if len(p.CSS) > maxCSSBytes {
		return errors.New("the CSS is larger than 20 KB")
	}
	// The CSS goes inside a <style> element. Ending that element early is the
	// one thing it could do that the page's policy would not contain.
	if strings.Contains(strings.ToLower(p.CSS), "</style") {
		return errors.New("the CSS cannot contain </style")
	}
	return nil
}

// LoginPage reads a domain's page, or the default when it has none.
func (m *Manager) LoginPage(ctx context.Context, domainID string) (LoginPage, error) {
	p := LoginPage{DomainID: domainID, Theme: "auto", PanelLink: true}
	var link int
	err := m.st.DB.QueryRowContext(ctx, `SELECT title, message, logo, accent, theme, css, panel_link FROM site_login_pages WHERE domain_id = ? AND server_id = ?`, domainID, m.st.ServerID).
		Scan(&p.Title, &p.Message, &p.Logo, &p.Accent, &p.Theme, &p.CSS, &link)
	p.PanelLink = err != nil || link == 1
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	return p, err
}

// SetLoginPage stores a domain's page.
func (m *Manager) SetLoginPage(ctx context.Context, actor string, p LoginPage) error {
	if err := p.Validate(); err != nil {
		return err
	}
	d, err := m.Domain(ctx, p.DomainID)
	if err != nil {
		return err
	}
	link := 0
	if p.PanelLink {
		link = 1
	}
	_, err = m.st.DB.ExecContext(ctx, `INSERT INTO site_login_pages (domain_id, server_id, title, message, logo, accent, theme, css, panel_link) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (domain_id) DO UPDATE SET title = excluded.title, message = excluded.message, logo = excluded.logo, accent = excluded.accent, theme = excluded.theme, css = excluded.css,
		panel_link = excluded.panel_link, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		p.DomainID, m.st.ServerID, p.Title, p.Message, p.Logo, p.Accent, p.Theme, p.CSS, link)
	if err == nil {
		_ = m.st.Audit(ctx, actor, "domain.login_page", d.Host, "")
	}
	return err
}

// ProtectedDomainFor is the enabled domain a sign-in on this host is for, or
// nil when no domain here asks for a login.
//
// An exact name wins over a wildcard, as it does in the router. A domain counts
// when the host itself is protected or any of its paths is: a site open at the
// root with a guarded /admin still needs somewhere to sign in.
func (m *Manager) ProtectedDomainFor(ctx context.Context, host string) *Domain {
	host = strings.ToLower(host)
	doms, err := m.Domains(ctx)
	if err != nil {
		return nil
	}
	var wild *Domain
	for i := range doms {
		d := &doms[i]
		if !d.Enabled || !d.AsksForLogin() {
			continue
		}
		if strings.EqualFold(d.Host, host) {
			return d
		}
		if suffix, ok := strings.CutPrefix(d.Host, "*."); ok && wild == nil {
			if label, ok := strings.CutSuffix(host, "."+strings.ToLower(suffix)); ok && label != "" && !strings.Contains(label, ".") {
				wild = d
			}
		}
	}
	return wild
}

// AsksForLogin reports whether any route on the domain is behind the gate.
func (d *Domain) AsksForLogin() bool {
	if d.TargetType == "panel" {
		return false
	}
	if d.Protect {
		return true
	}
	for _, l := range d.Locations {
		if l.Protect == "on" {
			return true
		}
	}
	return false
}
