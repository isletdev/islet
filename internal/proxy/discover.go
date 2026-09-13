package proxy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Finding what is already serving this machine.
//
// Somebody installing Islet on a server that is already in use has their sites
// described somewhere, and the shape of that somewhere depends on what they
// installed years ago rather than on anything we chose. Reading it beats asking
// them to type every hostname again, and guessing wrong is cheap: every site
// found is shown before anything is written.
//
// The parsers here are deliberately shallow. They look for the two facts a
// domain needs — which names to answer to, and what to forward to — and ignore
// everything else the file says. A reverse proxy configuration can express
// things Islet has no equivalent for, and half-understanding those is worse
// than not reading them, so a site whose upstream cannot be resolved is
// reported with what was found and left for the person to finish.

// Site is one virtual host found in somebody else's configuration.
type Site struct {
	Hosts    []string `json:"hosts"`
	Upstream string   `json:"upstream"` // what it forwards to, when it forwards
	Root     string   `json:"root"`     // a static directory, when it serves one
	Include  string   `json:"include,omitempty"`
	RawUp    string   `json:"rawUpstream,omitempty"` // an upstream that still held a variable
	TLS      bool     `json:"tls"`
	File     string   `json:"file"`
	// Source is the software whose configuration this came out of.
	Source string `json:"source,omitempty"`
	// RootStrip is true when the root forward replaces the path rather than
	// passing it through — nginx's trailing slash, Caddy's handle_path.
	RootStrip bool `json:"rootStrip,omitempty"`
	// Locations are the extra paths on this host that go somewhere else.
	Locations []SiteLocation `json:"locations,omitempty"`
	// NoRoot marks a host that forwards only on paths, with nothing serving
	// the root. Every product here allows that and Islet does not, so the
	// import has to decide what the root becomes and say that it did.
	NoRoot bool `json:"noRoot,omitempty"`
	// Skipped names the location blocks that were understood well enough to
	// see they forward somewhere, but not well enough to translate: regular
	// expressions, named locations, exact matches. Reporting them is the
	// point — a silent omission is how an import looks complete and is not.
	Skipped []string `json:"skipped,omitempty"`
}

// SiteLocation is one extra path on a host, forwarded somewhere of its own.
type SiteLocation struct {
	Path      string `json:"path"`
	Upstream  string `json:"upstream"`
	RawUp     string `json:"rawUpstream,omitempty"`
	StripPath bool   `json:"stripPath,omitempty"`
}

// NginxSite is the old name for a Site, kept so the nginx parser reads the way
// it did.
type NginxSite = Site

// Found groups the sites that came out of one product's configuration.
type Found struct {
	Source string   `json:"source"` // nginx, Caddy, Apache, Nginx Proxy Manager
	Files  []string `json:"files"`
	Sites  []Site   `json:"sites"`
}

// where each product keeps its virtual hosts.
//
// The Docker paths matter as much as the system ones: Nginx Proxy Manager is
// usually a container with its data in a named volume or a bind mount beside
// the compose file, and its generated files are the only place its hosts are
// written down in a form anything else can read.
var searchPaths = []struct {
	source string
	globs  []string
	parse  func(text, file string) []Site
}{
	{source: "nginx", parse: ParseNginx, globs: []string{
		"/etc/nginx/sites-enabled/*",
		"/etc/nginx/conf.d/*.conf",
		"/usr/local/etc/nginx/sites-enabled/*",
		"/usr/local/nginx/conf/conf.d/*.conf",
	}},
	{source: "Nginx Proxy Manager", parse: ParseNginx, globs: []string{
		"/var/lib/docker/volumes/*/_data/nginx/proxy_host/*.conf",
		"/opt/*/data/nginx/proxy_host/*.conf",
		"/opt/*/*/data/nginx/proxy_host/*.conf",
		"/root/*/data/nginx/proxy_host/*.conf",
		"/home/*/*/data/nginx/proxy_host/*.conf",
		"/data/nginx/proxy_host/*.conf",
	}},
	{source: "Caddy", parse: ParseCaddy, globs: []string{
		"/etc/caddy/Caddyfile",
		"/etc/caddy/conf.d/*",
		"/usr/local/etc/caddy/Caddyfile",
		"/var/lib/docker/volumes/*/_data/Caddyfile",
		"/opt/*/Caddyfile",
		"/opt/*/*/Caddyfile",
	}},
	{source: "Apache", parse: ParseApache, globs: []string{
		"/etc/apache2/sites-enabled/*",
		"/etc/httpd/conf.d/*.conf",
		"/etc/httpd/sites-enabled/*",
	}},
}

// Discover reads every location a known reverse proxy keeps its sites in.
//
// It only reads. Nothing is stopped, nothing is written, and a server that is
// currently answering for these names keeps answering until somebody moves it
// out of the way.
func Discover() []Found {
	if runtime.GOOS != "linux" {
		return []Found{}
	}
	out := []Found{}
	for _, p := range searchPaths {
		seen := map[string]bool{}
		f := Found{Source: p.source, Files: []string{}, Sites: []Site{}}
		for _, glob := range p.globs {
			paths, _ := filepath.Glob(glob)
			sort.Strings(paths)
			for _, path := range paths {
				if seen[path] {
					continue
				}
				seen[path] = true
				info, err := os.Stat(path)
				// A configuration file nobody could have written by hand is
				// not a configuration file.
				if err != nil || info.IsDir() || info.Size() > 4<<20 {
					continue
				}
				b, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				sites := p.parse(string(b), path)
				if len(sites) == 0 {
					continue
				}
				for i := range sites {
					sites[i].Source = p.source
				}
				f.Files = append(f.Files, path)
				f.Sites = append(f.Sites, sites...)
			}
		}
		if len(f.Sites) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// ParseText reads a pasted configuration, working out which product wrote it.
//
// Guessing beats a dropdown here: somebody pasting a file knows what it is and
// should not have to tell us twice, and the three formats are not alike enough
// to confuse.
func ParseText(text string) (Site []Site, source string) {
	switch {
	case strings.Contains(text, "<VirtualHost"):
		sites := ParseApache(text, "pasted")
		for i := range sites {
			sites[i].Source = "Apache"
		}
		return sites, "Apache"
	case serverBlockRe.MatchString(text) || strings.Contains(text, "proxy_pass"):
		sites := ParseNginx(text, "pasted")
		src := "nginx"
		if strings.Contains(text, "$forward_scheme") {
			src = "Nginx Proxy Manager"
		}
		for i := range sites {
			sites[i].Source = src
		}
		return sites, src
	default:
		sites := ParseCaddy(text, "pasted")
		for i := range sites {
			sites[i].Source = "Caddy"
		}
		return sites, "Caddy"
	}
}

// ---- Caddy ---------------------------------------------------------------

var (
	caddyReverse = regexp.MustCompile(`(?m)^\s*reverse_proxy\s+(?:([/*][^\s{]*)\s+)?([^\s{]+)`)
	caddyRoot    = regexp.MustCompile(`(?m)^\s*root\s+(?:\*\s+)?([^\s]+)`)
	// handle_path strips the matched prefix before proxying; handle and route
	// do not. That difference is the whole reason to tell them apart.
	caddyHandle = regexp.MustCompile(`(?m)^\s*(handle_path|handle|route)\s+([/*][^\s{]*)\s*\{`)
)

// caddyPath turns a Caddy path matcher into a prefix.
//
// "/" is returned for the matchers that mean the whole site — "/" and "/*" —
// which is a different answer from "this is a matcher Islet cannot express",
// and ok is what tells them apart. Collapsing the two would file a site's own
// root handler under the paths that were skipped.
func caddyPath(m string) (string, bool) {
	m = strings.TrimSpace(m)
	if m == "" || !strings.HasPrefix(m, "/") {
		return "", false
	}
	m = strings.TrimSuffix(m, "*")
	if strings.ContainsAny(m, "*? \t") {
		return "", false // a wildcard in the middle, or several matchers
	}
	if m = strings.TrimSuffix(m, "/"); m == "" {
		return "/", true
	}
	return m, true
}

// ParseCaddy reads site blocks out of a Caddyfile.
//
// A Caddyfile block is addresses, then a brace. The global options block has no
// addresses and is skipped, and so is a snippet, whose name is in parentheses.
func ParseCaddy(text, file string) []Site {
	var out []Site
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(stripComment(lines[i]))
		if !strings.HasSuffix(line, "{") {
			continue
		}
		head := strings.TrimSpace(strings.TrimSuffix(line, "{"))
		if head == "" || strings.HasPrefix(head, "(") {
			continue // global options, or a named snippet
		}

		depth, j := 1, i+1
		var body []string
		for ; j < len(lines) && depth > 0; j++ {
			depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
			if depth > 0 {
				body = append(body, lines[j])
			}
		}
		i = j - 1

		site := Site{File: file}
		for _, addr := range strings.Split(head, ",") {
			h := hostOfCaddyAddress(addr)
			if h != "" {
				site.Hosts = append(site.Hosts, h)
			}
			// Caddy gets a certificate for every name it serves unless the
			// address says http:// or names a port that is not 443.
			if !strings.HasPrefix(strings.TrimSpace(addr), "http://") {
				site.TLS = true
			}
		}
		block := strings.Join(body, "\n")
		// handle/handle_path/route blocks first, and cut out as they are read,
		// so the reverse_proxy inside one is not mistaken for the site's own.
		outer := block
		for {
			hm := caddyHandle.FindStringSubmatchIndex(outer)
			if hm == nil {
				break
			}
			kind := outer[hm[2]:hm[3]]
			path, ok := caddyPath(outer[hm[4]:hm[5]])
			inner, end := matchBrace(outer, hm[1])
			if rp := caddyReverse.FindStringSubmatch(inner); rp != nil {
				up := normaliseUpstream(rp[2])
				switch {
				case !ok:
					site.Skipped = append(site.Skipped, strings.TrimSpace(outer[hm[2]:hm[5]]))
				case path == "/":
					if site.Upstream == "" {
						site.Upstream, site.RootStrip = up, kind == "handle_path"
					}
				default:
					site.Locations = append(site.Locations, SiteLocation{
						Path: path, Upstream: up, StripPath: kind == "handle_path"})
				}
			}
			outer = outer[:hm[0]] + outer[end:]
		}
		// A reverse_proxy with an inline path matcher is a location too.
		for _, m := range caddyReverse.FindAllStringSubmatch(outer, -1) {
			up := normaliseUpstream(m[2])
			if m[1] == "" {
				if site.Upstream == "" {
					site.Upstream = up
				}
				continue
			}
			p, ok := caddyPath(m[1])
			switch {
			case !ok:
				site.Skipped = append(site.Skipped, strings.TrimSpace(m[1]))
			case p == "/":
				if site.Upstream == "" {
					site.Upstream = up
				}
			default:
				site.Locations = append(site.Locations, SiteLocation{Path: p, Upstream: up})
			}
		}
		if m := caddyRoot.FindStringSubmatch(outer); m != nil {
			site.Root = m[1]
		}
		if site.Upstream == "" && len(site.Locations) > 0 {
			site.NoRoot = true
		}
		if len(site.Hosts) > 0 {
			out = append(out, site)
		}
	}
	return out
}

// hostOfCaddyAddress turns an address into a hostname, or "" when it is not one.
func hostOfCaddyAddress(addr string) string {
	a := strings.TrimSpace(addr)
	a = strings.TrimPrefix(strings.TrimPrefix(a, "https://"), "http://")
	if i := strings.Index(a, "/"); i >= 0 {
		a = a[:i] // a path matcher is not part of the name
	}
	if i := strings.LastIndex(a, ":"); i > 0 {
		a = a[:i]
	}
	a = strings.ToLower(strings.TrimSpace(a))
	if a == "" || a == "localhost" || strings.HasPrefix(a, ":") || !strings.Contains(a, ".") {
		return ""
	}
	return a
}

// ---- Apache --------------------------------------------------------------

var (
	apacheVHost = regexp.MustCompile(`(?is)<VirtualHost[^>]*>(.*?)</VirtualHost>`)
	apacheName  = regexp.MustCompile(`(?mi)^\s*Server(?:Name|Alias)\s+(.+?)\s*$`)
	apacheProxy = regexp.MustCompile(`(?mi)^\s*ProxyPass\s+(?:/\S*\s+)?([^\s]+)`)
	// The path and the target together, which is what a location needs.
	apacheProxyPair = regexp.MustCompile(`(?mi)^\s*ProxyPass\s+(/\S*)\s+([^\s]+)`)
	apacheRoot      = regexp.MustCompile(`(?mi)^\s*DocumentRoot\s+(.+?)\s*$`)
	apacheSSL       = regexp.MustCompile(`(?mi)^\s*SSLEngine\s+on`)
)

// ParseApache reads virtual hosts out of an Apache configuration.
func ParseApache(text, file string) []Site {
	var out []Site
	for _, m := range apacheVHost.FindAllStringSubmatch(text, -1) {
		body := stripApacheComments(m[1])
		site := Site{File: file, TLS: apacheSSL.MatchString(body)}
		for _, n := range apacheName.FindAllStringSubmatch(body, -1) {
			for _, h := range strings.Fields(unquote(n[1])) {
				h = strings.ToLower(h)
				if h != "" && !strings.Contains(h, "*") && strings.Contains(h, ".") {
					site.Hosts = append(site.Hosts, h)
				}
			}
		}
		// Every ProxyPass is a path and a target. The one on / is the site;
		// the rest are locations. Apache replaces the matched prefix with the
		// target's path, so a target ending at the root strips the prefix.
		for _, p := range apacheProxyPair.FindAllStringSubmatch(body, -1) {
			path, target := strings.TrimSpace(p[1]), unquote(strings.TrimSpace(p[2]))
			if strings.EqualFold(target, "!") || target == "" {
				continue
			}
			up := normaliseUpstream(target)
			strip := apacheStrips(path, target)
			switch {
			case path == "/" || path == "":
				if site.Upstream == "" {
					site.Upstream, site.RootStrip = up, false
				}
			case strings.HasPrefix(path, "/") && !strings.ContainsAny(path, " \t*?"):
				site.Locations = append(site.Locations, SiteLocation{
					Path: strings.TrimSuffix(path, "/"), Upstream: up, StripPath: strip})
			default:
				site.Skipped = append(site.Skipped, path)
			}
		}
		if site.Upstream == "" && len(site.Locations) > 0 {
			site.NoRoot = true
		}
		if r := apacheRoot.FindStringSubmatch(body); r != nil {
			site.Root = unquote(r[1])
		}
		if len(site.Hosts) > 0 {
			out = append(out, dedupeHosts(site))
		}
	}
	return out
}

// apacheStrips says whether a ProxyPass replaces the matched prefix.
//
// Apache appends what is left of the request after the path to the target, so
// `ProxyPass /api http://app:3000/` turns /api/things into /things, while
// `ProxyPass /api http://app:3000/api` leaves it alone. Only the first of
// those has a Traefik equivalent, and it is stripPrefix.
func apacheStrips(path, target string) bool {
	i := strings.Index(target, "://")
	if i < 0 {
		return false
	}
	rest := target[i+3:]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return false // no path on the target: Apache passes the URI through
	}
	tail := strings.TrimSuffix(rest[slash:], "/")
	return tail == "" && strings.TrimSuffix(path, "/") != ""
}

// ---- shared --------------------------------------------------------------

func stripComment(line string) string {
	if i := strings.Index(line, "#"); i >= 0 {
		return line[:i]
	}
	return line
}

func stripApacheComments(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = stripComment(l)
	}
	return strings.Join(lines, "\n")
}

// normaliseUpstream gives a bare host:port the scheme the rest of the code
// expects, and drops a trailing path Islet has nowhere to put.
func normaliseUpstream(u string) string {
	u = strings.TrimSpace(unquote(u))
	if u == "" {
		return ""
	}
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	if i := strings.Index(u[strings.Index(u, "://")+3:], "/"); i >= 0 {
		u = u[:strings.Index(u, "://")+3+i]
	}
	return u
}

func dedupeHosts(s Site) Site {
	seen := map[string]bool{}
	out := s.Hosts[:0]
	for _, h := range s.Hosts {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	s.Hosts = out
	return s
}
