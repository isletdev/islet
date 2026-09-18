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
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/store"
)

// ErrExists marks a name that is already in use, so the API can answer 409
// rather than 400: a conflict is not a malformed request, and a client that
// retries a create needs to tell those apart.
var ErrExists = errors.New("already routed")

const (
	// Image is the pinned Traefik release.
	Image         = "traefik:v3.5"
	ContainerName = "islet-proxy"
	NetworkName   = "islet-proxy"
	mountPath     = "/etc/islet-proxy"

	// argsVersion changes when the container has to be started with different
	// flags. A running proxy whose label differs still serves every route, so
	// the panel offers to recreate it rather than doing it during an unrelated
	// save.
	argsVersion = "3"
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
	// Both halves have to be real ports. A value without a comma used to be
	// dropped in silence, so the proxy took 80 and 443 anyway, and a non-numeric
	// one reached "docker run -p" and failed there instead of here.
	if strings.TrimSpace(ports) != "" {
		a, b, ok := strings.Cut(ports, ",")
		pa, pb := strings.TrimSpace(a), strings.TrimSpace(b)
		if !ok || !validPort(pa) || !validPort(pb) {
			panic("ISLET_PROXY_PORTS must be two port numbers separated by a comma, for example 8880,8443; got " + ports)
		}
		m.httpP, m.httpsP = pa, pb
	}
	return m
}

// Subnet reports the address range of the proxy network, so the firewall can
// let the proxy container reach the daemon without opening the panel port to
// anyone else.
func (m *Manager) Subnet(ctx context.Context) string {
	out, err := m.run.Run(ctx, "system", "docker", "network", "inspect", NetworkName, "-f", "{{range .IPAM.Config}}{{.Subnet}} {{end}}")
	if err != nil {
		return ""
	}
	for _, f := range strings.Fields(out.Stdout) {
		if strings.Contains(f, "/") && !strings.Contains(f, ":") {
			return f
		}
	}
	return ""
}

// Ports reports the published HTTP and HTTPS ports, which the firewall needs
// so its rules match what is really listening.
func (m *Manager) Ports() (string, string) { return m.httpP, m.httpsP }

// PanelRouted says whether an enabled domain points at the panel.
func (m *Manager) PanelRouted(ctx context.Context) bool {
	return m.PanelHost(ctx) != ""
}

// PanelHost is the hostname an enabled domain reaches the panel on, or empty.
//
// It exists because the Host header of a browser request is not a usable
// address for anything running on the server. An agent started in a workspace
// runs here, and asking it to reach the panel at whatever name the person
// happened to type into their browser — a local alias, a name from their own
// hosts file — gives it an address that does not resolve. A domain routed to
// the panel resolves from both sides, which is the property that matters.
func (m *Manager) PanelHost(ctx context.Context) string {
	doms, err := m.Domains(ctx)
	if err != nil {
		return ""
	}
	for _, d := range doms {
		if d.TargetType == "panel" && d.Enabled {
			return d.Host
		}
	}
	return ""
}

func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n > 0 && n < 65536
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
	// NeedsRestart is true when the running container was started by an older
	// Islet with different flags. Routes still work; the container is simply
	// out of date, and recreating it is a deliberate act.
	NeedsRestart bool `json:"needsRestart"`
	// Problems are the reasons certificates are not being issued, in words.
	//
	// This exists because the failure it reports is silent everywhere else: a
	// proxy started without an ACME email has no certificate resolver, every
	// router asks for one that does not exist, and Traefik quietly serves its
	// own self-signed certificate for every site. The panel showed a running
	// proxy, an empty certificate list, and nothing to connect the two.
	Problems []string `json:"problems,omitempty"`
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
	if lbl, err := m.run.Run(ctx, "system", "docker", "inspect", "--format", "{{index .Config.Labels \"islet.proxy.args\"}}", ContainerName); err == nil {
		st.NeedsRestart = strings.TrimSpace(lbl.Stdout) != argsVersion
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
	// Without an email there is no ACME account, so Traefik is started with no
	// certificate resolver at all. Any domain asking for Let's Encrypt then
	// gets Traefik's built-in self-signed certificate and the browser calls it
	// insecure — which is a worse outcome than refusing, and one nothing in
	// the panel used to explain.
	if acmeEmail == "" {
		if doms, err := m.Domains(ctx); err == nil {
			var want []string
			for _, d := range doms {
				if d.Enabled && (d.TLS == "letsencrypt" || d.TLS == "letsencrypt-dns") {
					want = append(want, d.Host)
				}
			}
			if len(want) > 0 {
				return fmt.Errorf("%s %s a Let's Encrypt certificate, and Let's Encrypt needs an email address to issue one. Add one above and apply again, or set those domains to a self-signed certificate",
					hostList(want), verb(len(want), "asks for", "ask for"))
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(m.dir, "dynamic"), 0o750); err != nil {
		return err
	}
	if err := ensureACMEFile(filepath.Join(m.dir, "acme.json")); err != nil {
		return err
	}
	if err := m.writeMaintenancePage(); err != nil {
		return err
	}
	if err := m.ensureNetwork(ctx, actor); err != nil {
		return err
	}
	// Domains added before the proxy existed could not be attached then.
	if doms, err := m.Domains(ctx); err == nil {
		for _, d := range doms {
			if d.TargetType == "container" && d.Enabled {
				// A container that is gone must not stop the install.
				_ = m.Connect(ctx, actor, d.Target)
			}
		}
	}
	if _, err := m.run.Run(ctx, actor, "docker", "pull", Image); err != nil {
		return fmt.Errorf("pull traefik: %w", err)
	}

	// Keep the current proxy until the new one is proven. Removing it first
	// meant a failure here left nothing serving, and the usual failure is a
	// port already taken, which is exactly when people are reinstalling.
	previous := ContainerName + "-prev"
	_, _ = m.run.Run(ctx, actor, "docker", "rm", "-f", previous)
	hadOne := m.Status(ctx).Installed
	if hadOne {
		_, _ = m.run.Run(ctx, actor, "docker", "stop", ContainerName)
		if _, err := m.run.Run(ctx, actor, "docker", "rename", ContainerName, previous); err != nil {
			// Renaming failed, so the old one is still in place under its own
			// name and nothing has been lost. Remove it and carry on.
			_, _ = m.run.Run(ctx, actor, "docker", "rm", "-f", ContainerName)
			hadOne = false
		}
	}
	restore := func(cause error) error {
		_, _ = m.run.Run(ctx, actor, "docker", "rm", "-f", ContainerName)
		if hadOne {
			if _, err := m.run.Run(ctx, actor, "docker", "rename", previous, ContainerName); err == nil {
				_, _ = m.run.Run(ctx, actor, "docker", "start", ContainerName)
				return fmt.Errorf("%w; the previous proxy was put back and your sites are still served", cause)
			}
		}
		return cause
	}

	dir := m.dir
	args := []string{"run", "-d", "--name", ContainerName, "--restart", "unless-stopped",
		"--network", NetworkName,
		"-p", m.httpP + ":80", "-p", m.httpsP + ":443",
		"-v", dir + ":" + mountPath,
		"--add-host", "host.docker.internal:host-gateway",
		"--label", "islet.managed=proxy", "--label", "islet.proxy.args=" + argsVersion,
		Image,
		// Every route comes from the file provider below. Traefik's Docker
		// provider would add nothing, needs the daemon socket, and its client
		// speaks an API version Docker 29 refuses.
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
			_ = ensureACMEFile(filepath.Join(m.dir, "acme-dns.json"))
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
		return restore(fmt.Errorf("start traefik: %w", err))
	}
	// Starting is not running. A port collision or a rejected flag exits the
	// container a moment later, and that is the case the rollback exists for.
	time.Sleep(2 * time.Second)
	if res, err := m.run.Run(ctx, actor, "docker", "inspect", "--type", "container", "--format", "{{.State.Running}}", ContainerName); err != nil || strings.TrimSpace(res.Stdout) != "true" {
		logs, _ := m.run.Run(ctx, actor, "docker", "logs", "--tail", "20", ContainerName)
		return restore(fmt.Errorf("the new proxy did not stay up: %s", lastLines(logs.Stdout+logs.Stderr)))
	}
	_, _ = m.run.Run(ctx, actor, "docker", "rm", "-f", previous)
	_ = m.st.Audit(ctx, actor, "proxy.install", ContainerName, "email="+acmeEmail)
	return nil
}

// lastLines keeps the tail of command output short enough to show in the panel.
func lastLines(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, " | ")
}

// ensureACMEFile creates the certificate store if it is missing and, every
// time, makes sure nothing but the owner can read it.
//
// The mode is not housekeeping. Traefik refuses to load an ACME account from a
// file more permissive than 0600 — "permissions 777 ... are too open" — and
// its response is to drop the resolver from the list and carry on. Every
// router then asks for a resolver that no longer exists, so every site is
// served Traefik's own self-signed certificate and no certificate is ever
// requested. Nothing about that is loud: the proxy is running, the email is
// set, the certificate list is simply empty forever.
//
// Creating the file with 0600 was not enough, because the file outlives the
// install that made it: a restored backup, a copied data directory, an older
// Islet, or a bind mount that reports its own mode can all widen it.
func ensureACMEFile(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(path, []byte("{}"), 0o600)
	}
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o600 {
		// A failure here is not fatal on its own: a bind mount on Windows or a
		// filesystem without Unix modes cannot honour it, and Traefik does not
		// mind those. Diagnose reports it if Traefik does mind.
		_ = os.Chmod(path, 0o600)
	}
	return nil
}

// hostList prints a few host names and counts the rest.
func hostList(hosts []string) string {
	switch {
	case len(hosts) == 1:
		return hosts[0]
	case len(hosts) <= 3:
		return strings.Join(hosts[:len(hosts)-1], ", ") + " and " + hosts[len(hosts)-1]
	default:
		return fmt.Sprintf("%s and %d others", strings.Join(hosts[:2], ", "), len(hosts)-2)
	}
}

// verb picks the form that agrees with the number of hosts named.
func verb(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Diagnose says, in words, why certificates are not being issued.
//
// Two sources. The panel's own configuration, where a domain asking for a
// certificate Islet cannot request is knowable without asking anybody. And
// Traefik's log, which is where the answer already was — "Router uses a
// nonexistent certificate resolver", an ACME rejection, a challenge that timed
// out — behind a docker logs command nobody running a control panel should
// have to reach for.
func (m *Manager) Diagnose(ctx context.Context, st Status) []string {
	var out []string
	doms, err := m.Domains(ctx)
	if err != nil {
		return nil
	}
	var wantLE []string
	for _, d := range doms {
		if d.Enabled && (d.TLS == "letsencrypt" || d.TLS == "letsencrypt-dns") {
			wantLE = append(wantLE, d.Host)
		}
	}
	if len(wantLE) > 0 && st.Email == "" {
		out = append(out, fmt.Sprintf("%s %s a Let's Encrypt certificate, but no email address is set, so none can be requested and every one of them is served a self-signed certificate. Add an email under Settings and apply.",
			hostList(wantLE), verb(len(wantLE), "asks for", "ask for")))
	}
	var wantDNS []string
	for _, d := range doms {
		if d.Enabled && d.TLS == "letsencrypt-dns" {
			wantDNS = append(wantDNS, d.Host)
		}
	}
	if len(wantDNS) > 0 && st.DNSProvider == "" {
		out = append(out, fmt.Sprintf("%s %s to be issued over DNS, but no DNS provider is configured.",
			hostList(wantDNS), verb(len(wantDNS), "asks", "ask")))
	}
	if !st.Running {
		return out
	}

	// What Traefik itself has been saying. Recent only: an error from before
	// the last apply has usually been fixed by it.
	res, err := m.run.Run(ctx, "system", "docker", "logs", "--since", "30m", "--tail", "300", ContainerName)
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	// A resolver that was dropped explains every router that then cannot find
	// it, so the specific reason is collected first and the general complaint
	// is only reported when nothing better was found.
	generic := ""
	for _, line := range strings.Split(res.Stdout+res.Stderr, "\n") {
		clean := ansiRe.ReplaceAllString(line, "")
		var msg string
		switch {
		case strings.Contains(clean, "are too open"):
			msg = "Traefik refused to open its certificate store because the file is readable by more than its owner, so Let's Encrypt is switched off entirely and every site is served a self-signed certificate. Applying the settings again fixes the permissions."
		case strings.Contains(clean, "The ACME resolve is skipped"):
			msg = "Traefik dropped the Let's Encrypt resolver at startup: " + tail(clean)
		case strings.Contains(clean, "nonexistent certificate resolver"):
			generic = "Sites are asking for a certificate resolver Traefik does not have, so they are served a self-signed certificate. The usual cause is an empty email address or a certificate store Traefik refused to open."
			continue
		case strings.Contains(clean, "Unable to obtain ACME certificate"), strings.Contains(clean, "unable to generate a certificate"):
			msg = "Let's Encrypt refused a certificate: " + tail(clean)
		case strings.Contains(clean, "acme: error"), strings.Contains(clean, "urn:ietf:params:acme"):
			msg = "Let's Encrypt returned an error: " + tail(clean)
		case strings.Contains(clean, "port is already allocated"), strings.Contains(clean, "address already in use"):
			msg = "Another program already holds port 80 or 443. If nginx is still running, stop it with: systemctl disable --now nginx"
		default:
			continue
		}
		if !seen[msg] {
			seen[msg] = true
			out = append(out, msg)
		}
	}
	if generic != "" && len(out) == 0 {
		out = append(out, generic)
	}
	return out
}

// ansiRe strips the colour codes Traefik writes to a terminal.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// tail keeps the end of a log line, which is where the reason is.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 220 {
		s = "…" + s[len(s)-220:]
	}
	return s
}

// Remove stops and deletes the proxy container. Config and certificates stay.
func (m *Manager) Remove(ctx context.Context, actor string) error {
	_, err := m.run.Run(ctx, actor, "docker", "rm", "-f", ContainerName)
	_ = m.st.Audit(ctx, actor, "proxy.remove", ContainerName, "")
	return err
}

// ensureNetwork creates the proxy network if it does not exist yet. Domains
// can be added before the proxy is installed, and the attach must still work.
func (m *Manager) ensureNetwork(ctx context.Context, actor string) error {
	if _, err := m.run.Run(ctx, actor, "docker", "network", "inspect", NetworkName); err == nil {
		return nil
	}
	_, err := m.run.Run(ctx, actor, "docker", "network", "create", NetworkName)
	return err
}

// Connect attaches a container to the proxy network so Traefik can reach it.
func (m *Manager) Connect(ctx context.Context, actor, container string) error {
	res, err := m.run.Run(ctx, actor, "docker", "inspect", "--type", "container", "--format", "{{json .NetworkSettings.Networks}}", container)
	if err != nil {
		return err
	}
	if err := m.ensureNetwork(ctx, actor); err != nil {
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
	TLS         string `json:"tls"` // letsencrypt | letsencrypt-dns | self | none
	RedirectWWW bool   `json:"redirectWww"`
	// BasicAuth holds user:hash lines; the API accepts user:password and hashes
	// them on save. On an update an empty value means "leave what is stored
	// alone", because the panel never sends hashes back to a form it did not
	// show them in — see ClearBasicAuth for removing them.
	BasicAuth string `json:"basicAuth"`
	// ClearBasicAuth removes the stored users. It is a request field and is
	// never stored: "no value" and "remove the value" are different intentions
	// and one empty string cannot carry both.
	ClearBasicAuth bool   `json:"clearBasicAuth,omitempty"`
	IPAllowlist    string `json:"ipAllowlist"`
	RateLimit      int    `json:"rateLimit"`
	Headers        string `json:"headers"`
	Maintenance    bool   `json:"maintenance"`
	Protect        bool   `json:"protect"` // require a panel session (forward auth)
	// ProtectUsers narrows that to named accounts, comma separated. Empty
	// means any signed-in Islet user, which is what Protect alone always did.
	ProtectUsers string `json:"protectUsers"`
	Enabled      bool   `json:"enabled"`
	// PassHost sends the visitor's hostname to the app rather than the
	// upstream's own, which is what nginx and Nginx Proxy Manager do and what
	// anything checking Host or Origin — every WebSocket library among them —
	// expects to see.
	PassHost bool `json:"passHost"`
	// BlockExploits refuses the requests that only ever come from a scanner.
	BlockExploits bool   `json:"blockExploits"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
	// Locations are extra paths on this host that go somewhere else. The
	// domain's own target stays the root; a location is an exception to it.
	Locations []Location `json:"locations,omitempty"`
}

// Location is one path on a host forwarded somewhere of its own — what Nginx
// Proxy Manager calls a custom location and nginx calls a location block.
//
// Everything that guards the host guards its locations too: the same
// certificate, the same basic auth, allowlist, rate limit and headers. Those
// are properties of who may reach the name, and a path is not a different name.
type Location struct {
	ID         string `json:"id"`
	Path       string `json:"path"`       // /api
	TargetType string `json:"targetType"` // container | panel | url
	Target     string `json:"target"`
	Port       int    `json:"port"`
	// StripPath sends /api/things on as /things. nginx does this when
	// proxy_pass ends in a slash, Caddy when the block is handle_path.
	StripPath bool `json:"stripPath"`
	// Protect is inherit, on or off. It is three-state rather than a boolean
	// because a path has to be able to disagree with its host in both
	// directions: /admin asking for a login on an open site, and /webhooks
	// staying open on a protected one.
	Protect string `json:"protect"`
	// ProtectUsers is this path's own allow-list, comma separated, used only
	// when Protect is on. Empty means any signed-in Islet user.
	ProtectUsers string `json:"protectUsers"`
}

// validateTarget checks the three ways to name a backend. It is shared so a
// location cannot drift into accepting something a domain would refuse.
func validateTarget(kind string, target *string, port *int) error {
	switch kind {
	case "container":
		if !containerRe.MatchString(*target) {
			return errors.New("target must be a container name")
		}
		if *port < 1 || *port > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
	case "panel":
		*target, *port = "", 0
	case "url":
		if err := validateURLTarget(*target); err != nil {
			return err
		}
	default:
		return errors.New("targetType must be container, panel or url")
	}
	return nil
}

// validateURLTarget checks a URL a site will be proxied to.
//
// A prefix test for "http://" was the whole check, which accepted anything at
// all after it — including addresses that are not a backend anybody meant to
// publish. Two of those are refused here and nothing else is, because routing
// to something on this machine is an ordinary thing to want: a service on a
// port, reached through the host gateway, is how half the url targets in the
// wild are written.
//
// Link-local is the exception that has no innocent reading. 169.254.169.254 is
// the cloud metadata endpoint on every major provider — instance credentials,
// no authentication, trusted because only something on the machine can reach
// it. Publishing it at a hostname hands those credentials to the internet.
func validateURLTarget(target string) error {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("target must be an http(s) URL")
	}
	if u.Host == "" {
		return errors.New("that URL has no host")
	}
	ip := net.ParseIP(u.Hostname())
	if ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return errors.New("that address is link-local: on a cloud server it is the metadata endpoint, which holds this machine's credentials and must not be published")
	}
	return nil
}

// pointsAtPanel says whether a url target is the daemon itself.
//
// The panel has a target type of its own, which is admin-only and renders the
// forward-auth and header rules the panel needs. Reaching the same place by
// writing its address as a plain URL skipped all of that: it published the
// panel, past the admin-only check on the panel type and past the IP
// restriction, for anybody who could add a domain.
func pointsAtPanel(targetType, target, panelURL string) bool {
	if targetType != "url" {
		return false
	}
	p, err := url.Parse(panelURL)
	if err != nil {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return false
	}
	if u.Port() != p.Port() {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "host.docker.internal" || h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

var containerRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// validPath checks a path that will be interpolated into a Traefik rule.
//
// The characters refused here are the ones that would end the backtick literal
// the path sits inside and let the rest of the value become rule syntax, which
// is how one site could otherwise claim another site's host.
func validPath(p string) error {
	if !strings.HasPrefix(p, "/") {
		return errors.New("a path must start with /")
	}
	if strings.ContainsAny(p, "`$\\ \t\n\r\"'") {
		return errors.New("a path may not contain quotes, backticks or spaces")
	}
	return nil
}

// Validate normalises and checks one location.
func (l *Location) Validate() error {
	l.Path = strings.TrimSpace(l.Path)
	l.Path = strings.TrimSuffix(l.Path, "/")
	if l.Path == "" {
		return errors.New("a location needs a path, e.g. /api. The root is the domain's own target")
	}
	if err := validPath(l.Path); err != nil {
		return err
	}
	switch l.Protect {
	case "", "inherit":
		l.Protect = "inherit"
	case "on", "off":
	default:
		return errors.New("a location's protect must be inherit, on or off")
	}
	l.ProtectUsers = normUsers(l.ProtectUsers)
	return validateTarget(l.TargetType, &l.Target, &l.Port)
}

// Validate normalises and checks a domain.
func (d *Domain) Validate() error {
	d.Host = strings.ToLower(strings.TrimSpace(d.Host))
	if !hostRe.MatchString(d.Host) {
		return errors.New("host must be a valid domain name, e.g. app.example.com")
	}
	if err := validateTarget(d.TargetType, &d.Target, &d.Port); err != nil {
		return err
	}
	if d.TLS == "" {
		d.TLS = "letsencrypt"
	}
	// A wildcard has no other way to be issued: HTTP-01 would need a request
	// for every name under it. Recording that as the stored value rather than
	// working it out at render time means the page can say which challenge a
	// host uses without repeating the rule.
	if strings.HasPrefix(d.Host, "*.") && d.TLS == "letsencrypt" {
		d.TLS = "letsencrypt-dns"
	}
	switch d.TLS {
	case "letsencrypt", "letsencrypt-dns", "self", "none":
	default:
		return errors.New("tls must be letsencrypt, letsencrypt-dns, self or none")
	}
	if d.PathPrefix != "" {
		if err := validPath(d.PathPrefix); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for i := range d.Locations {
		if err := d.Locations[i].Validate(); err != nil {
			return fmt.Errorf("location %d: %w", i+1, err)
		}
		p := d.Locations[i].Path
		if seen[p] {
			return fmt.Errorf("two locations both claim %s", p)
		}
		seen[p] = true
	}
	if d.RateLimit < 0 {
		return errors.New("rateLimit must be >= 0")
	}
	d.ProtectUsers = normUsers(d.ProtectUsers)
	// A URL target used to be the one case that rewrote the Host, and nothing
	// said so. Anything that reads its own hostname — a WebSocket origin
	// check, an absolute redirect, a cookie domain — saw the upstream's name
	// instead of the one the visitor typed.
	if d.TargetType == "panel" {
		d.PassHost = true
	}
	for _, c := range splitList(d.IPAllowlist) {
		if !regexp.MustCompile(`^[0-9a-fA-F:.]+(/\d{1,3})?$`).MatchString(c) {
			return fmt.Errorf("bad CIDR %q", c)
		}
	}
	// Hash plaintext basic auth entries (user:password) into user:bcrypt.
	if d.ClearBasicAuth {
		d.BasicAuth = ""
	}
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

// normUsers tidies an allow-list: trimmed, lower-cased, deduplicated, in the
// order given. Stored as one string because it is read and written whole, and
// because the render path turns it straight back into a query parameter.
func normUsers(s string) string {
	seen := map[string]bool{}
	var out []string
	for _, u := range splitList(s) {
		u = strings.ToLower(u)
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return strings.Join(out, ",")
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

const cols = `id, host, target_type, target, port, path_prefix, tls, redirect_www, basic_auth, ip_allowlist, rate_limit, headers, maintenance, protect, protect_users, enabled, pass_host, block_exploits, created_at, updated_at`

func scan(sc interface{ Scan(...any) error }) (*Domain, error) {
	var d Domain
	var www, maint, prot, en, pass, block int
	if err := sc.Scan(&d.ID, &d.Host, &d.TargetType, &d.Target, &d.Port, &d.PathPrefix, &d.TLS, &www, &d.BasicAuth, &d.IPAllowlist, &d.RateLimit, &d.Headers, &maint, &prot, &d.ProtectUsers, &en, &pass, &block, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.RedirectWWW, d.Maintenance, d.Protect, d.Enabled = www == 1, maint == 1, prot == 1, en == 1
	d.PassHost, d.BlockExploits = pass == 1, block == 1
	return &d, nil
}

// Domains lists every domain on this server, with its locations.
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// One query for every location rather than one per domain: this runs on
	// every reconcile, which is every save.
	byDomain, err := m.allLocations(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Locations = byDomain[out[i].ID]
	}
	return out, nil
}

// allLocations groups every location on this server by its domain.
func (m *Manager) allLocations(ctx context.Context) (map[string][]Location, error) {
	rows, err := m.st.DB.QueryContext(ctx, `SELECT l.id, l.domain_id, l.path, l.target_type, l.target, l.port, l.strip_path, l.protect, l.protect_users
		FROM domain_locations l JOIN domains d ON d.id = l.domain_id
		WHERE d.server_id = ? ORDER BY l.domain_id, l.position, l.path`, m.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Location{}
	for rows.Next() {
		var l Location
		var domainID string
		var strip int
		if err := rows.Scan(&l.ID, &domainID, &l.Path, &l.TargetType, &l.Target, &l.Port, &strip, &l.Protect, &l.ProtectUsers); err != nil {
			return nil, err
		}
		l.StripPath = strip == 1
		out[domainID] = append(out[domainID], l)
	}
	return out, rows.Err()
}

// Domain loads one, with its locations.
func (m *Manager) Domain(ctx context.Context, id string) (*Domain, error) {
	d, err := scan(m.st.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM domains WHERE id = ? AND server_id = ?`, id, m.st.ServerID))
	if err != nil {
		return nil, err
	}
	rows, err := m.st.DB.QueryContext(ctx,
		`SELECT id, path, target_type, target, port, strip_path, protect, protect_users FROM domain_locations
		 WHERE domain_id = ? ORDER BY position, path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Location
		var strip int
		if err := rows.Scan(&l.ID, &l.Path, &l.TargetType, &l.Target, &l.Port, &strip, &l.Protect, &l.ProtectUsers); err != nil {
			return nil, err
		}
		l.StripPath = strip == 1
		d.Locations = append(d.Locations, l)
	}
	return d, rows.Err()
}

// saveLocations replaces a domain's locations with the ones given.
//
// Replacing rather than diffing: the set is small, the form sends the whole
// list, and a diff would have to answer what an edited path means — a rename
// or a new location — for no gain anybody can see.
func (m *Manager) saveLocations(ctx context.Context, domainID string, locs []Location) error {
	tx, err := m.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM domain_locations WHERE domain_id = ?`, domainID); err != nil {
		return err
	}
	for i, l := range locs {
		if l.ID == "" {
			l.ID = newID()
		}
		strip := 0
		if l.StripPath {
			strip = 1
		}
		if l.Protect == "" {
			l.Protect = "inherit"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_locations
			(id, domain_id, path, target_type, target, port, strip_path, protect, protect_users, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			l.ID, domainID, l.Path, l.TargetType, l.Target, l.Port, strip, l.Protect, l.ProtectUsers, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// writeDomain inserts or updates the row and nothing else. It is separate from
// Save so the storage round trip can be tested without Docker: every column
// added since has had to be listed in four places — the select, the scan, the
// insert and the update — and a value missed in one of them is invisible until
// somebody saves a domain.
func (m *Manager) writeDomain(ctx context.Context, d *Domain) error {
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	if d.ID == "" {
		d.ID = newID()
		_, err := m.st.DB.ExecContext(ctx, `INSERT INTO domains (id, server_id, host, target_type, target, port, path_prefix, tls, redirect_www, basic_auth, ip_allowlist, rate_limit, headers, maintenance, protect, protect_users, enabled, pass_host, block_exploits)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.ID, m.st.ServerID, d.Host, d.TargetType, d.Target, d.Port, d.PathPrefix, d.TLS, b(d.RedirectWWW), d.BasicAuth, d.IPAllowlist, d.RateLimit, d.Headers, b(d.Maintenance), b(d.Protect), d.ProtectUsers, b(d.Enabled), b(d.PassHost), b(d.BlockExploits))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("that host is %w", ErrExists)
			}
			return err
		}
	} else {
		// An empty value on an update means "unchanged". The editor opens with
		// the field blank — it has hashes to show and showing them would be
		// worse — so taking that blank literally quietly deleted the users of
		// any site whose port or certificate somebody came to change.
		if d.BasicAuth == "" && !d.ClearBasicAuth {
			var stored string
			if err := m.st.DB.QueryRowContext(ctx, `SELECT basic_auth FROM domains WHERE id = ? AND server_id = ?`, d.ID, m.st.ServerID).Scan(&stored); err == nil {
				d.BasicAuth = stored
			}
		}
		_, err := m.st.DB.ExecContext(ctx, `UPDATE domains SET host=?, target_type=?, target=?, port=?, path_prefix=?, tls=?, redirect_www=?, basic_auth=?, ip_allowlist=?, rate_limit=?, headers=?, maintenance=?, protect=?, protect_users=?, enabled=?, pass_host=?, block_exploits=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id = ? AND server_id = ?`,
			d.Host, d.TargetType, d.Target, d.Port, d.PathPrefix, d.TLS, b(d.RedirectWWW), d.BasicAuth, d.IPAllowlist, d.RateLimit, d.Headers, b(d.Maintenance), b(d.Protect), d.ProtectUsers, b(d.Enabled), b(d.PassHost), b(d.BlockExploits), d.ID, m.st.ServerID)
		if err != nil {
			return err
		}
	}
	return nil
}

// Save inserts or updates a domain, connects the target to the proxy
// network, and reconciles the Traefik config.
func (m *Manager) Save(ctx context.Context, actor string, d *Domain) (*Domain, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	// The panel has a target type of its own, and it is admin-only. Writing the
	// daemon's own address as a url target reached the same place without it.
	if pointsAtPanel(d.TargetType, d.Target, m.panel) {
		return nil, errors.New("that address is the panel: choose the panel target type instead, which is what renders its login and header rules")
	}
	for _, l := range d.Locations {
		if pointsAtPanel(l.TargetType, l.Target, m.panel) {
			return nil, errors.New("a location points at the panel: choose the panel target type instead")
		}
	}
	// Validate has already turned a wildcard asking for Let's Encrypt into a
	// DNS-01 request, so this one check covers both ways of getting here.
	if d.TLS == "letsencrypt-dns" && m.DNSProvider(ctx) == "" {
		if strings.HasPrefix(d.Host, "*.") {
			return nil, errors.New("a wildcard certificate can only be issued over DNS, which needs a DNS provider: set one under Domains, Settings, or use a self-signed certificate")
		}
		return nil, errors.New("the DNS challenge needs a DNS provider: set one under Domains, Settings, or issue this certificate over HTTP instead")
	}
	if err := m.writeDomain(ctx, d); err != nil {
		return nil, err
	}
	if err := m.saveLocations(ctx, d.ID, d.Locations); err != nil {
		return nil, err
	}
	// A location's container needs the proxy network as much as the root's
	// does, and forgetting one is a 502 on that path only — the hardest kind
	// of half-working to diagnose from the outside.
	attach := []string{}
	if d.TargetType == "container" {
		attach = append(attach, d.Target)
	}
	for _, l := range d.Locations {
		if l.TargetType == "container" {
			attach = append(attach, l.Target)
		}
	}
	done := map[string]bool{}
	for _, name := range attach {
		if done[name] {
			continue
		}
		done[name] = true
		if err := m.Connect(ctx, actor, name); err != nil {
			return nil, fmt.Errorf("attach %s to the proxy network: %w", name, err)
		}
	}
	if err := m.Reconcile(ctx); err != nil {
		return nil, err
	}
	detail := d.TargetType + " " + d.Target
	// Who may reach a site belongs in the log that answers "who changed what",
	// and a row that records only where it points does not say it.
	if p := d.ProtectSummary(); p != "" {
		detail += " " + p
	}
	_ = m.st.Audit(ctx, actor, "domain.save", d.Host, detail)
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
// maxAccessLog is how large Traefik's access log may get before it is rotated.
const maxAccessLog = 64 << 20

// RotateAccessLog keeps Traefik's access log from filling the disk.
//
// Traefik has no rotation of its own: it writes until told otherwise, and what
// it expects to be told is SIGUSR1, which nobody was sending. At roughly 300
// bytes a line, one busy site writes about ten gigabytes a year into the data
// directory — where Islet's own disk alert then fires at 85%, about a file
// Islet created, from a page that does not offer to show it.
//
// One previous file is kept, so the most recent traffic can still be read while
// the total stays bounded at about twice the cap. USR1 makes Traefik reopen the
// path it was given, which is what turns the rename into a rotation rather than
// a file that is still being written to by its old handle.
func (m *Manager) RotateAccessLog(ctx context.Context) {
	path := filepath.Join(m.dir, "access.log")
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < maxAccessLog {
		return
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return
	}
	// If the signal does not land — no proxy running, a Docker that is not
	// answering — Traefik keeps writing to the renamed file and the next start
	// opens a fresh one. Nothing is lost either way, which is why this does not
	// report an error anybody has to act on. The move above is the part that
	// bounds the disk, so it happens either way.
	if m.run == nil {
		return
	}
	_, _ = m.run.Run(ctx, "system", "docker", "kill", "--signal=USR1", ContainerName)
}

func (m *Manager) Reconcile(ctx context.Context) error {
	// Recreating the proxy is the most destructive routine operation here, and
	// saving a domain is not a reason to do it: domains arrive through the file
	// written below, which Traefik watches. An upgrade that changed the
	// container's flags is reported on the Domains page instead, so a person
	// chooses when to take the sites down for a moment.
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

// exploitRule matches requests that only ever come from somebody looking for a
// way in, as a Traefik rule expression.
//
// What it covers: the paths a scanner asks for within seconds of a new host
// appearing in a certificate transparency log. Secrets committed by accident
// (.env, .git, .aws, .ssh), source control and editor leftovers, backups left
// in the web root, two PHP packages with famous remote-execution holes, and
// the two HTTP methods whose only purpose is to echo a request back.
//
// What it does not cover, and will not pretend to: query strings. Nginx Proxy
// Manager's equivalent greps the whole query for SQL and XSS fragments, and
// Traefik's rule language can only match a named parameter, so the same thing
// cannot be written here. It is also the half that ages worst — it blocks the
// payloads somebody wrote down years ago while breaking search boxes and
// message bodies that legitimately contain the word "select". An application
// that parameterises its queries is not helped by it, and one that does not is
// not saved by it.
//
// /.well-known/ stays reachable on purpose: ACME answers its challenge there,
// and blocking it would stop certificates from being issued.
var exploitRule = strings.Join([]string{
	"PathRegexp(`^/\\.(env|git|svn|hg|aws|ssh|docker|idea|vscode|DS_Store)(/|$)`)",
	"PathRegexp(`(?i)^/(vendor/phpunit|_ignition/execute-solution|server-status|server-info)(/|$)`)",
	"PathRegexp(`(?i)\\.(sql|bak|old|orig|save|swp|swo|rej)$`)",
	"PathRegexp(`(?i)(/etc/passwd|/proc/self/environ)`)",
	"Method(`TRACE`)",
	"Method(`TRACK`)",
}, " || ")

// tlsFor gives the TLS block every router on a host shares, and says when the
// host is on plain HTTP instead.
//
// A wildcard is checked as well as the stored value: rows written before the
// certificate method became a choice carry "letsencrypt" and still cannot be
// issued any way but over DNS.
func tlsFor(d Domain, wildcard bool) (map[string]any, bool) {
	switch {
	case d.TLS == "none":
		return nil, true
	case d.TLS == "self":
		return map[string]any{}, false
	case d.TLS == "letsencrypt-dns" || wildcard:
		block := map[string]any{"certResolver": "letsencrypt-dns"}
		if wildcard {
			// Ask for the wildcard and the bare name together, so
			// example.com and anything.example.com share one certificate.
			block["domains"] = []map[string]any{{"main": d.Host, "sans": []string{strings.TrimPrefix(d.Host, "*.")}}}
		}
		return block, false
	default:
		return map[string]any{"certResolver": "letsencrypt"}, false
	}
}

// backendService builds the load balancer for one target, whichever of the
// three kinds it is. Shared so a location cannot reach a backend in a way the
// root could not.
func backendService(kind, target string, port int, panelURL string, passHost bool) map[string]any {
	switch kind {
	case "container":
		return map[string]any{"loadBalancer": map[string]any{
			"servers": []map[string]string{{"url": fmt.Sprintf("http://%s:%d", target, port)}}, "passHostHeader": passHost}}
	case "panel":
		return map[string]any{"loadBalancer": map[string]any{
			"servers": []map[string]string{{"url": panelURL}}, "serversTransport": "islet-insecure", "passHostHeader": true}}
	default: // url
		svc := map[string]any{"servers": []map[string]string{{"url": target}}, "passHostHeader": passHost}
		if strings.HasPrefix(target, "https://") {
			svc["serversTransport"] = "islet-insecure"
		}
		return map[string]any{"loadBalancer": svc}
	}
}

// Render turns domains into Traefik dynamic YAML. panelURL is the daemon as
// reachable from inside the proxy container.
func Render(domains []Domain, panelURL string) ([]byte, error) {
	routers := map[string]any{}
	services := map[string]any{}
	middlewares := map[string]any{
		// Traefik sets X-Forwarded-Proto to "ws" or "wss" on a WebSocket
		// upgrade, rather than to the scheme the browser actually used. Almost
		// nothing downstream understands those: Plug.SSL, Rails, Django and
		// Laravel all compare the value against "https" and, finding "wss",
		// conclude the connection is plain and redirect to HTTPS — which is
		// where the request already came from. The socket then fails as a
		// redirect loop while every ordinary request on the same host works,
		// which is a very hard thing to see from the outside.
		//
		// Islet knows which scheme the route answers on, so it says so, and
		// the value is the same for every request whether or not it upgrades.
		"islet-proto-https": map[string]any{"headers": map[string]any{
			"customRequestHeaders": map[string]string{"X-Forwarded-Proto": "https"}}},
		"islet-proto-http": map[string]any{"headers": map[string]any{
			"customRequestHeaders": map[string]string{"X-Forwarded-Proto": "http"}}},
		// Compression, except on the content types that are streams.
		//
		// Traefik's compressor holds the first kilobyte back to decide whether
		// compressing is worth it, and a stream's first kilobyte can be minutes
		// of work away — so an endpoint that writes a line at a time reached the
		// browser as nothing at all, then as a gateway timeout. It affected
		// every streaming endpoint here: container logs, a Compose deploy, a
		// ping, and the assistant, all of which exist precisely to be watched
		// while they run.
		//
		// no-transform on the response says the same thing to the CDN in front,
		// which has its own compressor and its own opinion about buffering.
		"islet-compress": map[string]any{"compress": map[string]any{
			"excludedContentTypes": []string{"text/event-stream", "application/x-ndjson"},
		}},
		"islet-security-headers": map[string]any{"headers": map[string]any{"stsSeconds": 31536000, "stsIncludeSubdomains": true, "browserXssFilter": true, "contentTypeNosniff": true}},
		"islet-https-redirect":   map[string]any{"redirectScheme": map[string]any{"scheme": "https", "permanent": true}},
		// X-Islet-User is what an app is told to trust, and on a route with no
		// gate in front of it the value is simply whatever the visitor typed.
		// Traefik replaces these two on a protected route — that was measured,
		// not assumed — but an open route, or an open path on a protected
		// host, hands them straight to the backend. An empty value in
		// customRequestHeaders deletes the header, so every route starts
		// without one and only the gate can put it back.
		"islet-strip-identity": map[string]any{"headers": map[string]any{
			"customRequestHeaders": map[string]string{"X-Islet-User": "", "X-Islet-Role": ""},
		}},
	}
	transports := map[string]any{"islet-insecure": map[string]any{"insecureSkipVerify": true}}

	// One forwardAuth middleware per protected route, with that route's
	// allow-list written into the address it calls.
	//
	// The alternative was one shared middleware and a lookup — the daemon
	// matching the forwarded host and path back to a rule on every request, to
	// arrive at a list it had itself just written into this file. Carrying the
	// list in the address keeps the decision where the rule is: no second
	// matcher to disagree with Traefik's, no cache to go stale, and a
	// configuration anybody can read to see who is allowed where. The address
	// is written by the daemon and never derived from a request, so a visitor
	// has no way to influence it.
	protectMW := func(id, users string) string {
		addr := panelURL + "/_islet/auth"
		if users != "" {
			addr += "?u=" + url.QueryEscape(users)
		}
		middlewares[id+"-protect"] = map[string]any{"forwardAuth": map[string]any{
			"address": addr, "trustForwardHeader": true,
			"authResponseHeaders": []string{"X-Islet-User", "X-Islet-Role"},
			"tls":                 map[string]any{"insecureSkipVerify": true},
		}}
		return id + "-protect"
	}

	for _, d := range domains {
		if !d.Enabled {
			continue
		}
		name := "d-" + d.ID
		hostRule := fmt.Sprintf("Host(`%s`)", d.Host)
		wildcard := strings.HasPrefix(d.Host, "*.")
		// Traefik ranks routers by rule length unless a priority is set, and the
		// wildcard regex is always longer than an exact host. Without these two
		// numbers one wildcard swallows every named site in the zone, the panel
		// included.
		priority := 100
		if wildcard {
			hostRule = fmt.Sprintf("HostRegexp(`^[a-z0-9-]+\\.%s$`)", strings.ReplaceAll(strings.TrimPrefix(d.Host, "*."), ".", "\\."))
			priority = 1
		}
		rule := hostRule
		if d.PathPrefix != "" {
			rule += fmt.Sprintf(" && PathPrefix(`%s`)", d.PathPrefix)
		}
		// First in the chain: a later middleware that reads the scheme has to
		// see the corrected value.
		proto := "islet-proto-https"
		if d.TLS == "none" {
			proto = "islet-proto-http"
		}
		mws := []string{proto, "islet-strip-identity", "islet-compress", "islet-security-headers"}
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
		// Kept separately so a location can leave it out: "off" on a path of a
		// protected host is how a webhook receiver stays reachable.
		base := mws
		hostProtect := ""
		// Never in front of the panel itself. The login the gate sends people
		// to is served by this very host, so the redirect lands back on the
		// protected router and loops — and it buys nothing: the panel has
		// asked for a session on every page since the first release.
		if d.Protect && d.TargetType != "panel" {
			hostProtect = protectMW(name, d.ProtectUsers)
			mws = append(mws, hostProtect)
		}
		// The TLS block is settled once and reused by every router on this
		// host — the root, each location, and www. A location presenting a
		// different certificate from the page that links to it is not a
		// configuration anybody wants.
		tlsBlock, onHTTP := tlsFor(d, wildcard)
		router := map[string]any{"rule": rule, "entryPoints": []string{"websecure"}, "middlewares": mws}
		if onHTTP {
			router["entryPoints"] = []string{"web"}
		} else {
			router["tls"] = tlsBlock
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
		router["priority"] = priority
		routers[name] = router

		// A location is a longer rule on the same host, and has to win against
		// the root. Traefik ranks by rule length only when no priority is set,
		// and the root already sets one, so every location sets its own.
		//
		// The bands keep two invariants. Inside a host, a longer path beats a
		// shorter one, so /api/v2 is not swallowed by /api. Across hosts, any
		// route on an exact name still beats any route on a wildcard, so a
		// location under *.example.com cannot take a path away from a site
		// named outright.
		for i, l := range d.Locations {
			ln := fmt.Sprintf("%s-l%d", name, i)
			lrule := hostRule + fmt.Sprintf(" && PathPrefix(`%s`)", l.Path)
			// PathPrefix is a string prefix, not a path prefix: a rule for
			// /hooks also claims /hooksecret. Harmless while a location only
			// chooses a backend, and a hole the moment it opens something the
			// host protects — "open /hooks" would open every path that starts
			// with those letters. So a location that lets somebody in the host
			// would turn away matches that path and what is under it, nothing
			// else. Locations that only tighten keep nginx's prefix matching,
			// which is what people import from and rely on.
			if relaxes(d, l) {
				// And an encoded slash sends the request back to the host's
				// own rule. Traefik normalises ".." and "%2e%2e" before it
				// matches, but "%2F" is not a separator to a proxy and may be
				// one to the application behind it — so /hooks/..%2fadmin
				// would arrive at an app that resolves it as /admin, through a
				// path opened for something else. Matching on the escaped form
				// is what makes this expressible at all, and falling through to
				// the gate rather than refusing means a signed-in visitor with
				// a genuine %2F in a URL still gets there.
				lrule = hostRule + fmt.Sprintf(" && (Path(`%s`) || PathPrefix(`%s/`)) && !PathRegexp(`(?i)%%2f|%%5c`)", l.Path, l.Path)
			}
			lpri := priority + 1 + min(len(l.Path), 98)

			// A copy, not a reslice: appending to a shared backing array
			// would let one location's middleware appear on another's.
			//
			// The nearer rule wins. A path that says nothing follows its host,
			// which is what every location did before locations could carry a
			// rule of their own.
			lmws := append([]string{}, base...)
			switch l.Protect {
			case "on":
				lmws = append(lmws, protectMW(ln, l.ProtectUsers))
			case "off":
			default:
				if hostProtect != "" {
					lmws = append(lmws, hostProtect)
				}
			}
			if l.StripPath {
				middlewares[ln+"-strip"] = map[string]any{"stripPrefix": map[string]any{"prefixes": []string{l.Path}}}
				lmws = append(lmws, ln+"-strip")
			}
			lrouter := map[string]any{"rule": lrule, "entryPoints": []string{"websecure"}, "middlewares": lmws, "priority": lpri}
			if onHTTP {
				lrouter["entryPoints"] = []string{"web"}
			} else {
				lrouter["tls"] = tlsBlock
				routers[ln+"-http"] = map[string]any{"rule": lrule, "entryPoints": []string{"web"}, "middlewares": []string{"islet-https-redirect"}, "service": "noop@internal", "priority": lpri}
			}
			// Maintenance covers the whole host. A location still answering
			// while the site it belongs to says it is down is worse than
			// either state on its own.
			if d.Maintenance {
				lrouter["middlewares"] = append(lmws, name+"-maint")
				lrouter["service"] = "islet-panel"
			} else {
				lrouter["service"] = ln
				services[ln] = backendService(l.TargetType, l.Target, l.Port, panelURL, d.PassHost)
			}
			routers[ln] = lrouter
		}

		// www rides on its own router and its own certificate: a missing www
		// DNS record then breaks only the redirect, not this host's TLS.
		if d.RedirectWWW && !wildcard {
			middlewares[name+"-www"] = map[string]any{"redirectRegex": map[string]any{"regex": fmt.Sprintf(`^https?://www\.%s/(.*)`, regexp.QuoteMeta(d.Host)), "replacement": fmt.Sprintf("https://%s/${1}", d.Host), "permanent": true}}
			wwwRule := fmt.Sprintf("Host(`www.%s`)", d.Host)
			wwwRouter := map[string]any{"rule": wwwRule, "entryPoints": []string{"websecure"}, "middlewares": []string{name + "-www"}, "service": "noop@internal"}
			if onHTTP {
				wwwRouter["entryPoints"] = []string{"web"}
			} else {
				// www gets a certificate of its own rather than a SAN on the
				// site's: a missing www record then breaks the redirect only,
				// not the host people actually visit.
				if d.TLS == "letsencrypt-dns" {
					wwwRouter["tls"] = map[string]any{"certResolver": "letsencrypt-dns"}
				} else {
					wwwRouter["tls"] = map[string]any{"certResolver": "letsencrypt"}
				}
				if d.TLS == "self" {
					wwwRouter["tls"] = map[string]any{}
				}
				routers[name+"-www-http"] = map[string]any{"rule": wwwRule, "entryPoints": []string{"web"}, "middlewares": []string{name + "-www"}, "service": "noop@internal"}
			}
			routers[name+"-www"] = wwwRouter
		}

		services[name] = backendService(d.TargetType, d.Target, d.Port, panelURL, d.PassHost)

		// Requests that only ever come from somebody looking for a way in.
		// A router of its own rather than a middleware, because Traefik has no
		// middleware that refuses a request and the rule language is the only
		// place this can be expressed at all.
		if d.BlockExploits {
			bn := name + "-block"
			brule := hostRule + " && (" + exploitRule + ")"
			middlewares[bn] = map[string]any{"replacePath": map[string]any{"path": "/_islet/blocked"}}
			broute := map[string]any{
				"rule": brule, "entryPoints": []string{"websecure"},
				"middlewares": []string{bn}, "service": "islet-panel",
				// Above every location on this host, so a blocked path cannot
				// be reached through one of them either.
				"priority": priority + 200,
			}
			if onHTTP {
				broute["entryPoints"] = []string{"web"}
			} else {
				broute["tls"] = tlsBlock
			}
			routers[bn] = broute
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

// Forgotten says what taking a username off the allow-lists did.
type Forgotten struct {
	// Rules is how many lists named them.
	Rules int
	// Widened names the routes whose list held nobody else. An empty list
	// means "any signed-in user", so those routes still ask for a login but no
	// longer ask for a particular person — which is a change worth telling
	// somebody about rather than making quietly.
	Widened []string
}

// ForgetUser takes a username off every allow-list and reconciles, so a
// deleted account leaves no rule behind that a new account with the same name
// would inherit.
//
// Nothing breaks if it is never called — a name nobody can sign in as opens
// nothing — but a list that still names somebody who left is a list nobody can
// read honestly, and usernames are reusable.
func (m *Manager) ForgetUser(ctx context.Context, actor, username string) (Forgotten, error) {
	var res Forgotten
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return res, nil
	}
	doms, err := m.Domains(ctx)
	if err != nil {
		return res, err
	}
	drop := func(list string) (string, bool) {
		var kept []string
		for _, u := range splitList(list) {
			if u != username {
				kept = append(kept, u)
			}
		}
		out := strings.Join(kept, ",")
		return out, out != list
	}
	for i := range doms {
		d := doms[i]
		changed := false
		if out, ok := drop(d.ProtectUsers); ok {
			d.ProtectUsers, changed = out, true
			res.Rules++
			if out == "" {
				res.Widened = append(res.Widened, d.Host)
			}
		}
		for j := range d.Locations {
			if out, ok := drop(d.Locations[j].ProtectUsers); ok {
				d.Locations[j].ProtectUsers, changed = out, true
				res.Rules++
				if out == "" && d.Locations[j].Protect == "on" {
					res.Widened = append(res.Widened, d.Host+d.Locations[j].Path)
				}
			}
		}
		if !changed {
			continue
		}
		if err := m.writeDomain(ctx, &d); err != nil {
			return res, err
		}
		if err := m.saveLocations(ctx, d.ID, d.Locations); err != nil {
			return res, err
		}
		_ = m.st.Audit(ctx, actor, "domain.protect.forget", d.Host, "removed "+username)
	}
	if res.Rules > 0 {
		return res, m.Reconcile(ctx)
	}
	return res, nil
}

// ProtectionEquals reports whether two versions of a domain would let the same
// people reach the same paths. It normalises as it goes, so a list retyped in
// another order or with different spacing is not a change.
//
// It exists so the API can hold changes to who may reach a site to a higher bar
// than changes to where it points, without the HTTP layer having to know how
// protection is spelled.
func (d *Domain) ProtectionEquals(o *Domain) bool {
	if o == nil {
		o = &Domain{}
	}
	if d.Protect != o.Protect || normUsers(d.ProtectUsers) != normUsers(o.ProtectUsers) {
		return false
	}
	type rule struct{ protect, users string }
	at := func(x *Domain) map[string]rule {
		m := map[string]rule{}
		for _, l := range x.Locations {
			p := l.Protect
			if p == "" {
				p = "inherit"
			}
			m[strings.TrimSuffix(strings.TrimSpace(l.Path), "/")] = rule{p, normUsers(l.ProtectUsers)}
		}
		return m
	}
	a, b := at(d), at(o)
	// A path that is gone no longer lets anybody anywhere, so only paths
	// present in the new version need to agree — but one that appears with a
	// rule of its own is a change, which the length check below catches.
	if len(a) != len(b) {
		return false
	}
	for path, ra := range a {
		if rb, ok := b[path]; !ok || ra != rb {
			return false
		}
	}
	return true
}

// ProtectSummary says who may reach what, for the audit log.
func (d *Domain) ProtectSummary() string {
	parts := []string{}
	who := func(users string) string {
		if users == "" {
			return "any signed-in user"
		}
		return users
	}
	if d.Protect {
		parts = append(parts, "protect="+who(d.ProtectUsers))
	}
	for _, l := range d.Locations {
		switch l.Protect {
		case "on":
			parts = append(parts, l.Path+"="+who(l.ProtectUsers))
		case "off":
			parts = append(parts, l.Path+"=open")
		}
	}
	return strings.Join(parts, " ")
}

// relaxes reports whether a location admits somebody the host's own rule would
// turn away — by asking for no login at all, or by naming people the host does
// not. It decides how the location's rule is matched, because only a rule that
// weakens the gate is dangerous when it claims more paths than it looks like.
func relaxes(d Domain, l Location) bool {
	if !d.Protect {
		return false // nothing to relax
	}
	switch l.Protect {
	case "off":
		return true
	case "on":
		host := splitList(d.ProtectUsers)
		if len(host) == 0 {
			return false // the host already admits anyone signed in
		}
		loc := splitList(l.ProtectUsers)
		if len(loc) == 0 {
			return true // this path admits anyone signed in; the host does not
		}
		for _, u := range loc {
			if !slices.Contains(host, u) {
				return true
			}
		}
	}
	return false
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
