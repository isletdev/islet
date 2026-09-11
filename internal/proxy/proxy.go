// Package proxy runs Traefik as a managed container and turns the domains
// table into Traefik's dynamic configuration. Islet owns ports 80 and 443
// through this container; every app reached by a domain goes through it.
package proxy

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/store"
)

const (
	// Image is the pinned Traefik release.
	Image         = "traefik:v3.5"
	ContainerName = "islet-proxy"
	NetworkName   = "islet-proxy"
	mountPath     = "/etc/islet-proxy"
)

// Manager owns the proxy container and its config directory.
type Manager struct {
	run    *cmdrun.Runner
	st     *store.Store
	dir    string // <dataDir>/proxy
	httpP  string
	httpsP string
	panel  string // URL of the daemon as seen from the proxy container

	// Keys encrypts DNS provider credentials at rest (set by main).
	Keys *auth.Keys
}

// New builds the manager. httpPort/httpsPort default to 80/443; a dev box
// can override them with ISLET_PROXY_PORTS=8080,8443.
func New(run *cmdrun.Runner, st *store.Store, dataDir, ports string) *Manager {
	m := &Manager{run: run, st: st, dir: filepath.Join(dataDir, "proxy"), httpP: "80", httpsP: "443", panel: "https://host.docker.internal:9443"}
	if a, b, ok := strings.Cut(ports, ","); ok {
		m.httpP, m.httpsP = strings.TrimSpace(a), strings.TrimSpace(b)
	}
	return m
}

// SetPanelURL tells the proxy how to reach the daemon (scheme and port).
func (m *Manager) SetPanelURL(scheme, port string) {
	m.panel = scheme + "://host.docker.internal:" + port
}

// DNSProviders maps a provider name to the environment variables Traefik's
// ACME DNS-01 challenge needs (lego provider names).
var DNSProviders = map[string][]string{
	"cloudflare":   {"CF_DNS_API_TOKEN"},
	"hetzner":      {"HETZNER_API_KEY"},
	"digitalocean": {"DO_AUTH_TOKEN"},
	"route53":      {"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION"},
	"desec":        {"DESEC_TOKEN"},
	"porkbun":      {"PORKBUN_API_KEY", "PORKBUN_SECRET_API_KEY"},
	"gandiv5":      {"GANDIV5_PERSONAL_ACCESS_TOKEN"},
	"ovh":          {"OVH_ENDPOINT", "OVH_APPLICATION_KEY", "OVH_APPLICATION_SECRET", "OVH_CONSUMER_KEY"},
	"namecheap":    {"NAMECHEAP_API_USER", "NAMECHEAP_API_KEY"},
	"linode":       {"LINODE_TOKEN"},
	"vultr":        {"VULTR_API_KEY"},
	"scaleway":     {"SCW_SECRET_KEY", "SCW_PROJECT_ID"},
}

// SetDNS stores the DNS-01 provider and its credentials (encrypted). An
// empty provider clears it. Install applies the change.
func (m *Manager) SetDNS(ctx context.Context, provider string, env map[string]string) error {
	if provider == "" {
		_ = m.st.SetSetting(ctx, "proxy.dns_provider", "")
		_ = m.st.SetSetting(ctx, "proxy.dns_env", "")
		return nil
	}
	keys, ok := DNSProviders[provider]
	if !ok {
		return errors.New("unknown DNS provider")
	}
	old := m.dnsEnv(ctx)
	for _, k := range keys {
		if strings.TrimSpace(env[k]) == "" {
			if old[k] != "" {
				env[k] = old[k] // keep stored secret
				continue
			}
			return errors.New(k + " is required for " + provider)
		}
	}
	if m.Keys == nil {
		return errors.New("no key store for credentials")
	}
	b, _ := json.Marshal(env)
	enc, err := m.Keys.Encrypt(b)
	if err != nil {
		return err
	}
	if err := m.st.SetSetting(ctx, "proxy.dns_provider", provider); err != nil {
		return err
	}
	return m.st.SetSetting(ctx, "proxy.dns_env", base64.StdEncoding.EncodeToString(enc))
}

// DNSProvider returns the configured provider name, if any.
func (m *Manager) DNSProvider(ctx context.Context) string {
	v, _, _ := m.st.Setting(ctx, "proxy.dns_provider")
	return v
}

func (m *Manager) dnsEnv(ctx context.Context) map[string]string {
	out := map[string]string{}
	v, _, _ := m.st.Setting(ctx, "proxy.dns_env")
	if v == "" || m.Keys == nil {
		return out
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return out
	}
	p, err := m.Keys.Decrypt(b)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(p, &out)
	return out
}

// Status describes the proxy container.
type Status struct {
	DNSProvider string `json:"dnsProvider"`
	Installed   bool   `json:"installed"`
	Running     bool   `json:"running"`
	Image       string `json:"image"`
	Email       string `json:"acmeEmail"`
	HTTPPort    string `json:"httpPort"`
	HTTPSPort   string `json:"httpsPort"`
	Error       string `json:"error,omitempty"`
}

// Status inspects the proxy container.
func (m *Manager) Status(ctx context.Context) Status {
	st := Status{Image: Image, HTTPPort: m.httpP, HTTPSPort: m.httpsP}
	st.Email, _, _ = m.st.Setting(ctx, "proxy.acme_email")
	st.DNSProvider = m.DNSProvider(ctx)
	res, err := m.run.Run(ctx, "system", "docker", "inspect", "--type", "container", "--format", "{{.State.Running}} {{.Config.Image}}", ContainerName)
	if err != nil {
		return st
	}
	f := strings.Fields(res.Stdout)
	st.Installed = true
	st.Running = len(f) > 0 && f[0] == "true"
	if len(f) > 1 {
		st.Image = f[1]
	}
	return st
}

// Install pulls Traefik, creates the network and config dir, and starts the
// container. Re-running recreates the container with current settings.
func (m *Manager) Install(ctx context.Context, actor, acmeEmail string) error {
	if acmeEmail != "" {
		if !strings.Contains(acmeEmail, "@") {
			return errors.New("ACME email is not valid")
		}
		if err := m.st.SetSetting(ctx, "proxy.acme_email", acmeEmail); err != nil {
			return err
		}
	} else {
		acmeEmail, _, _ = m.st.Setting(ctx, "proxy.acme_email")
	}
	if err := os.MkdirAll(filepath.Join(m.dir, "dynamic"), 0o750); err != nil {
		return err
	}
	acme := filepath.Join(m.dir, "acme.json")
	if _, err := os.Stat(acme); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(acme, []byte("{}"), 0o600); err != nil {
			return err
		}
	}
	if err := m.writeMaintenancePage(); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, actor, "docker", "network", "inspect", NetworkName); err != nil {
		if _, err := m.run.Run(ctx, actor, "docker", "network", "create", NetworkName); err != nil {
			return err
		}
	}
	if _, err := m.run.Run(ctx, actor, "docker", "pull", Image); err != nil {
		return fmt.Errorf("pull traefik: %w", err)
	}
	_, _ = m.run.Run(ctx, actor, "docker", "rm", "-f", ContainerName)

	sock := "/var/run/docker.sock"
	if runtime.GOOS == "windows" {
		sock = "//var/run/docker.sock"
	}
	dir := m.dir
	args := []string{"run", "-d", "--name", ContainerName, "--restart", "unless-stopped",
		"--network", NetworkName,
		"-p", m.httpP + ":80", "-p", m.httpsP + ":443",
		"-v", sock + ":/var/run/docker.sock:ro",
		"-v", dir + ":" + mountPath,
		"--add-host", "host.docker.internal:host-gateway",
		"--label", "islet.managed=proxy", "--label", "islet.proxy.args=2",
		Image,
		"--providers.docker=true", "--providers.docker.exposedbydefault=false", "--providers.docker.network=" + NetworkName,
		"--providers.file.directory=" + mountPath + "/dynamic", "--providers.file.watch=true",
		"--entrypoints.web.address=:80",
		"--entrypoints.websecure.address=:443",
		"--entrypoints.websecure.http.tls=true",
		"--api.dashboard=false", "--ping=true", "--log.level=INFO",
		"--accesslog=true", "--accesslog.filepath=" + mountPath + "/access.log", "--accesslog.bufferingsize=100",
	}
	if acmeEmail != "" {
		args = append(args,
			"--certificatesresolvers.letsencrypt.acme.email="+acmeEmail,
			"--certificatesresolvers.letsencrypt.acme.storage="+mountPath+"/acme.json",
			"--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web",
		)
		if prov := m.DNSProvider(ctx); prov != "" {
			acmeDNS := filepath.Join(m.dir, "acme-dns.json")
			if _, err := os.Stat(acmeDNS); errors.Is(err, os.ErrNotExist) {
				_ = os.WriteFile(acmeDNS, []byte("{}"), 0o600)
			}
			args = append(args,
				"--certificatesresolvers.letsencrypt-dns.acme.email="+acmeEmail,
				"--certificatesresolvers.letsencrypt-dns.acme.storage="+mountPath+"/acme-dns.json",
				"--certificatesresolvers.letsencrypt-dns.acme.dnschallenge.provider="+prov,
				"--certificatesresolvers.letsencrypt-dns.acme.dnschallenge.resolvers=1.1.1.1:53,8.8.8.8:53",
			)
			// Credentials go in as environment variables before the image name.
			var envArgs []string
			for k, v := range m.dnsEnv(ctx) {
				envArgs = append(envArgs, "-e", k+"="+v)
			}
			for i, a := range args {
				if a == Image {
					args = append(append(append([]string{}, args[:i]...), envArgs...), args[i:]...)
					break
				}
			}
		}
	}
	if _, err := m.run.Run(ctx, actor, "docker", args...); err != nil {
		return fmt.Errorf("start traefik: %w", err)
	}
	_ = m.st.Audit(ctx, actor, "proxy.install", ContainerName, "email="+acmeEmail)
	return nil
}

// Remove stops and deletes the proxy container. Config and certificates stay.
func (m *Manager) Remove(ctx context.Context, actor string) error {
	_, err := m.run.Run(ctx, actor, "docker", "rm", "-f", ContainerName)
	_ = m.st.Audit(ctx, actor, "proxy.remove", ContainerName, "")
	return err
}

// Connect attaches a container to the proxy network so Traefik can reach it.
func (m *Manager) Connect(ctx context.Context, actor, container string) error {
	res, err := m.run.Run(ctx, actor, "docker", "inspect", "--type", "container", "--format", "{{json .NetworkSettings.Networks}}", container)
	if err != nil {
		return err
	}
	var nets map[string]any
	if json.Unmarshal([]byte(res.Stdout), &nets) == nil {
		if _, ok := nets[NetworkName]; ok {
			return nil
		}
	}
	_, err = m.run.Run(ctx, actor, "docker", "network", "connect", NetworkName, container)
	return err
}

// ---- domains ----

var hostRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// Domain is one routed host.
type Domain struct {
	ID          string `json:"id"`
	Host        string `json:"host"`
	TargetType  string `json:"targetType"` // container | panel | url
	Target      string `json:"target"`
	Port        int    `json:"port"`
	PathPrefix  string `json:"pathPrefix"`
	TLS         string `json:"tls"` // letsencrypt | self | none
	RedirectWWW bool   `json:"redirectWww"`
	BasicAuth   string `json:"basicAuth"` // user:hash lines; API accepts user:password and hashes
	IPAllowlist string `json:"ipAllowlist"`
	RateLimit   int    `json:"rateLimit"`
	Headers     string `json:"headers"`
	Maintenance bool   `json:"maintenance"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// Validate normalises and checks a domain.
func (d *Domain) Validate() error {
	d.Host = strings.ToLower(strings.TrimSpace(d.Host))
	if !hostRe.MatchString(d.Host) {
		return errors.New("host must be a valid domain name, e.g. app.example.com")
	}
	switch d.TargetType {
	case "container":
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(d.Target) {
			return errors.New("target must be a container name")
		}
		if d.Port < 1 || d.Port > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
	case "panel":
		d.Target, d.Port = "", 0
	case "url":
		if !strings.HasPrefix(d.Target, "http://") && !strings.HasPrefix(d.Target, "https://") {
			return errors.New("target must be an http(s) URL")
		}
	default:
		return errors.New("targetType must be container, panel or url")
	}
	if d.TLS == "" {
		d.TLS = "letsencrypt"
	}
	if d.TLS != "letsencrypt" && d.TLS != "self" && d.TLS != "none" {
		return errors.New("tls must be letsencrypt, self or none")
	}
	if d.PathPrefix != "" && !strings.HasPrefix(d.PathPrefix, "/") {
		return errors.New("pathPrefix must start with /")
	}
	if d.RateLimit < 0 {
		return errors.New("rateLimit must be >= 0")
	}
	for _, c := range splitList(d.IPAllowlist) {
		if !regexp.MustCompile(`^[0-9a-fA-F:.]+(/\d{1,3})?$`).MatchString(c) {
			return fmt.Errorf("bad CIDR %q", c)
		}
	}
	// Hash plaintext basic auth entries (user:password) into user:bcrypt.
	var lines []string
	for _, l := range strings.Split(d.BasicAuth, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		user, pw, ok := strings.Cut(l, ":")
		if !ok || user == "" {
			return errors.New("basic auth lines must be user:password")
		}
		if strings.HasPrefix(pw, "$2") {
			lines = append(lines, l)
			continue
		}
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		lines = append(lines, user+":"+string(h))
	}
	d.BasicAuth = strings.Join(lines, "\n")
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

const cols = `id, host, target_type, target, port, path_prefix, tls, redirect_www, basic_auth, ip_allowlist, rate_limit, headers, maintenance, enabled, created_at, updated_at`

func scan(sc interface{ Scan(...any) error }) (*Domain, error) {
	var d Domain
	var www, maint, en int
	if err := sc.Scan(&d.ID, &d.Host, &d.TargetType, &d.Target, &d.Port, &d.PathPrefix, &d.TLS, &www, &d.BasicAuth, &d.IPAllowlist, &d.RateLimit, &d.Headers, &maint, &en, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.RedirectWWW, d.Maintenance, d.Enabled = www == 1, maint == 1, en == 1
	return &d, nil
}

// Domains lists every domain on this server.
func (m *Manager) Domains(ctx context.Context) ([]Domain, error) {
	rows, err := m.st.DB.QueryContext(ctx, `SELECT `+cols+` FROM domains WHERE server_id = ? ORDER BY host`, m.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// Domain loads one.
func (m *Manager) Domain(ctx context.Context, id string) (*Domain, error) {
	return scan(m.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM domains WHERE id = ? AND server_id = ?`, id, m.st.ServerID))
}

// Save inserts or updates a domain, connects the target to the proxy
// network, and reconciles the Traefik config.
func (m *Manager) Save(ctx context.Context, actor string, d *Domain) (*Domain, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	if strings.HasPrefix(d.Host, "*.") && d.TLS == "letsencrypt" && m.DNSProvider(ctx) == "" {
		return nil, errors.New("wildcard certificates need a DNS provider: set one on the proxy card first, or use a self-signed certificate")
	}
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	if d.ID == "" {
		d.ID = newID()
		_, err := m.st.DB.ExecContext(ctx, `INSERT INTO domains (id, server_id, host, target_type, target, port, path_prefix, tls, redirect_www, basic_auth, ip_allowlist, rate_limit, headers, maintenance, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.ID, m.st.ServerID, d.Host, d.TargetType, d.Target, d.Port, d.PathPrefix, d.TLS, b(d.RedirectWWW), d.BasicAuth, d.IPAllowlist, d.RateLimit, d.Headers, b(d.Maintenance), b(d.Enabled))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("that host is already routed")
			}
			return nil, err
		}
	} else {
		_, err := m.st.DB.ExecContext(ctx, `UPDATE domains SET host=?, target_type=?, target=?, port=?, path_prefix=?, tls=?, redirect_www=?, basic_auth=?, ip_allowlist=?, rate_limit=?, headers=?, maintenance=?, enabled=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id = ? AND server_id = ?`,
			d.Host, d.TargetType, d.Target, d.Port, d.PathPrefix, d.TLS, b(d.RedirectWWW), d.BasicAuth, d.IPAllowlist, d.RateLimit, d.Headers, b(d.Maintenance), b(d.Enabled), d.ID, m.st.ServerID)
		if err != nil {
			return nil, err
		}
	}
	if d.TargetType == "container" {
		if err := m.Connect(ctx, actor, d.Target); err != nil {
			return nil, fmt.Errorf("attach %s to the proxy network: %w", d.Target, err)
		}
	}
	if err := m.Reconcile(ctx); err != nil {
		return nil, err
	}
	_ = m.st.Audit(ctx, actor, "domain.save", d.Host, d.TargetType+" "+d.Target)
	return m.Domain(ctx, d.ID)
}

// Delete removes a domain and reconciles.
func (m *Manager) Delete(ctx context.Context, actor, id string) error {
	d, err := m.Domain(ctx, id)
	if err != nil {
		return err
	}
	if _, err := m.st.DB.ExecContext(ctx, `DELETE FROM domains WHERE id = ? AND server_id = ?`, id, m.st.ServerID); err != nil {
		return err
	}
	_ = m.st.Audit(ctx, actor, "domain.delete", d.Host, "")
	return m.Reconcile(ctx)
}

// ---- dynamic config ----

// Reconcile writes the Traefik dynamic config from the domains table.
func (m *Manager) Reconcile(ctx context.Context) error {
	if res, err := m.run.Run(ctx, "system", "docker", "inspect", "--format", "{{index .Config.Labels \"islet.proxy.args\"}}", ContainerName); err == nil && strings.TrimSpace(res.Stdout) != "2" {
		if err := m.Install(ctx, "system", ""); err != nil {
			return fmt.Errorf("upgrade proxy: %w", err)
		}
	}
	domains, err := m.Domains(ctx)
	if err != nil {
		return err
	}
	cfg, err := Render(domains, m.panel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(m.dir, "dynamic"), 0o750); err != nil {
		return err
	}
	p := filepath.Join(m.dir, "dynamic", "islet.yml")
	if err := os.WriteFile(p+".tmp", cfg, 0o640); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// Render turns domains into Traefik dynamic YAML. panelURL is the daemon as
// reachable from inside the proxy container.
func Render(domains []Domain, panelURL string) ([]byte, error) {
	routers := map[string]any{}
	services := map[string]any{}
	middlewares := map[string]any{
		"islet-compress":         map[string]any{"compress": map[string]any{}},
		"islet-security-headers": map[string]any{"headers": map[string]any{"stsSeconds": 31536000, "stsIncludeSubdomains": true, "browserXssFilter": true, "contentTypeNosniff": true}},
		"islet-https-redirect":   map[string]any{"redirectScheme": map[string]any{"scheme": "https", "permanent": true}},
	}
	transports := map[string]any{"islet-insecure": map[string]any{"insecureSkipVerify": true}}

	for _, d := range domains {
		if !d.Enabled {
			continue
		}
		name := "d-" + d.ID
		rule := fmt.Sprintf("Host(`%s`)", d.Host)
		wildcard := strings.HasPrefix(d.Host, "*.")
		if wildcard {
			rule = fmt.Sprintf("HostRegexp(`^[a-z0-9-]+\\.%s$`)", strings.ReplaceAll(strings.TrimPrefix(d.Host, "*."), ".", "\\."))
		}
		if d.RedirectWWW {
			rule = fmt.Sprintf("(Host(`%s`) || Host(`www.%s`))", d.Host, d.Host)
		}
		if d.PathPrefix != "" {
			rule += fmt.Sprintf(" && PathPrefix(`%s`)", d.PathPrefix)
		}
		mws := []string{"islet-compress", "islet-security-headers"}
		if d.RedirectWWW {
			middlewares[name+"-www"] = map[string]any{"redirectRegex": map[string]any{"regex": fmt.Sprintf(`^https?://www\.%s/(.*)`, regexp.QuoteMeta(d.Host)), "replacement": fmt.Sprintf("https://%s/${1}", d.Host), "permanent": true}}
			mws = append(mws, name+"-www")
		}
		if d.IPAllowlist != "" {
			middlewares[name+"-ipallow"] = map[string]any{"ipAllowList": map[string]any{"sourceRange": splitList(d.IPAllowlist)}}
			mws = append(mws, name+"-ipallow")
		}
		if d.RateLimit > 0 {
			middlewares[name+"-ratelimit"] = map[string]any{"rateLimit": map[string]any{"average": d.RateLimit, "burst": d.RateLimit * 2}}
			mws = append(mws, name+"-ratelimit")
		}
		if d.BasicAuth != "" {
			middlewares[name+"-auth"] = map[string]any{"basicAuth": map[string]any{"users": strings.Split(d.BasicAuth, "\n")}}
			mws = append(mws, name+"-auth")
		}
		if d.Headers != "" {
			h := map[string]string{}
			for _, l := range strings.Split(d.Headers, "\n") {
				if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) != "" {
					h[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			if len(h) > 0 {
				middlewares[name+"-headers"] = map[string]any{"headers": map[string]any{"customResponseHeaders": h}}
				mws = append(mws, name+"-headers")
			}
		}
		router := map[string]any{"rule": rule, "entryPoints": []string{"websecure"}, "middlewares": mws}
		switch d.TLS {
		case "letsencrypt":
			router["tls"] = map[string]any{"certResolver": "letsencrypt"}
			if wildcard {
				router["tls"] = map[string]any{"certResolver": "letsencrypt-dns", "domains": []map[string]any{{"main": d.Host, "sans": []string{strings.TrimPrefix(d.Host, "*.")}}}}
			}
		case "self":
			router["tls"] = map[string]any{}
		case "none":
			router["entryPoints"] = []string{"web"}
		}
		if d.TLS != "none" {
			// Plain HTTP for this host redirects to HTTPS; ACME challenges are answered before routing.
			routers[name+"-http"] = map[string]any{"rule": rule, "entryPoints": []string{"web"}, "middlewares": []string{"islet-https-redirect"}, "service": "noop@internal"}
		}
		if d.Maintenance {
			// Send everything to the daemon's public maintenance page.
			middlewares[name+"-maint"] = map[string]any{"replacePath": map[string]any{"path": "/_islet/maintenance"}}
			router["middlewares"] = append(mws, name+"-maint")
			router["service"] = "islet-panel"
		} else {
			router["service"] = name
		}
		routers[name] = router

		var url string
		switch d.TargetType {
		case "container":
			url = fmt.Sprintf("http://%s:%d", d.Target, d.Port)
			services[name] = map[string]any{"loadBalancer": map[string]any{"servers": []map[string]string{{"url": url}}, "passHostHeader": true}}
		case "panel":
			services[name] = map[string]any{"loadBalancer": map[string]any{"servers": []map[string]string{{"url": panelURL}}, "serversTransport": "islet-insecure", "passHostHeader": true}}
		case "url":
			svc := map[string]any{"servers": []map[string]string{{"url": d.Target}}, "passHostHeader": false}
			if strings.HasPrefix(d.Target, "https://") {
				svc["serversTransport"] = "islet-insecure"
			}
			services[name] = map[string]any{"loadBalancer": svc}
		}
	}
	// The daemon itself, reachable from the container through the host gateway.
	services["islet-panel"] = map[string]any{"loadBalancer": map[string]any{"servers": []map[string]string{{"url": panelURL}}, "serversTransport": "islet-insecure", "passHostHeader": true}}

	// Traefik's file provider rejects empty maps ("routers cannot be a standalone element"), so omit them.
	httpCfg := map[string]any{"services": services, "middlewares": middlewares, "serversTransports": transports}
	if len(routers) > 0 {
		httpCfg["routers"] = routers
	}
	return yaml.Marshal(map[string]any{"http": httpCfg})
}

// ---- certificates ----

// Cert is one certificate in acme.json.
type Cert struct {
	Domain   string    `json:"domain"`
	SANs     []string  `json:"sans"`
	NotAfter time.Time `json:"notAfter"`
	Issuer   string    `json:"issuer"`
}

// Certificates parses Traefik's acme.json.
func (m *Manager) Certificates() ([]Cert, error) {
	out := []Cert{}
	for _, f := range []string{"acme.json", "acme-dns.json"} {
		list, err := m.certsFrom(filepath.Join(m.dir, f))
		if err != nil && f == "acme.json" {
			return nil, err
		}
		out = append(out, list...)
	}
	return out, nil
}

func (m *Manager) certsFrom(path string) ([]Cert, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]struct {
		Certificates []struct {
			Domain struct {
				Main string   `json:"main"`
				SANs []string `json:"sans"`
			} `json:"domain"`
			Certificate string `json:"certificate"`
		} `json:"Certificates"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := []Cert{}
	for _, res := range raw {
		for _, c := range res.Certificates {
			cert := Cert{Domain: c.Domain.Main, SANs: c.Domain.SANs}
			if der, err := base64.StdEncoding.DecodeString(c.Certificate); err == nil {
				if na, iss, ok := parseLeaf(der); ok {
					cert.NotAfter, cert.Issuer = na, iss
				}
			}
			out = append(out, cert)
		}
	}
	return out, nil
}

// MaintenancePage is served by the daemon at /_islet/maintenance.
const MaintenancePage = `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Down for maintenance</title>
<style>body{margin:0;font:16px/1.5 system-ui,sans-serif;background:#0A0A0A;color:#FAFAFA;display:grid;place-items:center;min-height:100vh}main{max-width:32rem;padding:2rem}h1{font-size:1.5rem;margin:0 0 .5rem}p{color:#A3A3A3;margin:0}</style>
<main><h1>Down for maintenance</h1><p>This site is being updated and will be back shortly.</p></main>`

func (m *Manager) writeMaintenancePage() error { return nil }

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
