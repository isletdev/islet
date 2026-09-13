package proxy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var (
	serverBlockRe = regexp.MustCompile(`(?s)server\s*\{`)
	directiveRe   = regexp.MustCompile(`(?:^|[\s{;])(server_name|proxy_pass|root|listen|include)\s+([^;{}]+);`)
	// Nginx Proxy Manager writes `set $server "app";` and then
	// `proxy_pass $forward_scheme://$server:$port;`, so the variables have to
	// be resolved before the upstream means anything.
	setRe = regexp.MustCompile(`(?:^|[\s{;])set\s+\$([A-Za-z0-9_]+)\s+([^;{}]+);`)
	varRe = regexp.MustCompile(`\$\{?([A-Za-z0-9_]+)\}?`)
	// The head of a location block: an optional modifier and then the path.
	locationRe = regexp.MustCompile(`(?:^|[\s{;])location\s+([^{]+)\{`)
)

// resolveVars substitutes `set` variables into a directive value. Unknown
// variables are left alone so the caller can see what was missing.
func resolveVars(val string, vars map[string]string) string {
	for range 5 { // nested definitions, e.g. set $a "$b"
		out := varRe.ReplaceAllStringFunc(val, func(m string) string {
			if v, ok := vars[varRe.FindStringSubmatch(m)[1]]; ok {
				return v
			}
			return m
		})
		if out == val {
			break
		}
		val = out
	}
	return val
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 1 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// matchBrace returns the index just past the `}` closing a block whose opening
// brace has already been consumed, and the block's body.
func matchBrace(text string, from int) (body string, end int) {
	depth, i := 1, from
	for i < len(text) && depth > 0 {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	return text[from : i-boolToInt(depth == 0)], i
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// upstreamOf reads the proxy_pass out of a block, resolving variables and
// reporting whether nginx would strip the location prefix.
//
// The trailing slash is the whole of that rule and it is easy to miss:
// `proxy_pass http://app:3000/` sends /api/things on as /things, and
// `proxy_pass http://app:3000` sends it as /api/things. Traefik spells the
// first one stripPrefix, so the slash has to be read before it is trimmed.
func upstreamOf(block string, vars map[string]string) (upstream, raw string, strip bool) {
	for _, m := range directiveRe.FindAllStringSubmatch(block, -1) {
		if m[1] != "proxy_pass" {
			continue
		}
		val := unquote(resolveVars(strings.TrimSpace(m[2]), vars))
		if strings.Contains(val, "$") {
			if raw == "" {
				raw = val
			}
			continue
		}
		if upstream == "" {
			// Only a bare scheme://host[:port]/ means "replace the prefix".
			// A path after it (proxy_pass http://app/v1/) is a rewrite Islet
			// has no equivalent for, so it is left on the upstream and the
			// prefix is passed through.
			trimmed := strings.TrimSuffix(val, "/")
			if _, _, ok := UpstreamParts(trimmed); ok && strings.HasSuffix(val, "/") {
				strip = true
			}
			upstream = trimmed
		}
	}
	return upstream, raw, strip
}

// npmUpstream rebuilds the upstream from the variables Nginx Proxy Manager
// sets, for the host types where it keeps the proxy_pass in an included
// snippet and the file only carries $server and $port.
func npmUpstream(vars map[string]string) string {
	if vars["server"] == "" || vars["port"] == "" {
		return ""
	}
	scheme := vars["forward_scheme"]
	if scheme != "http" && scheme != "https" {
		scheme = "http"
	}
	return scheme + "://" + vars["server"] + ":" + vars["port"]
}

// locationPath reads the head of a location block.
//
// ok is false for the forms Islet cannot express: a regular expression
// (`location ~ \.php$`), a named location (`location @fallback`), and an
// exact match (`location = /health`), which is a different rule from a prefix
// and would quietly start matching more than it did.
func locationPath(head string) (path string, exact bool, ok bool) {
	h := strings.TrimSpace(head)
	switch {
	case strings.HasPrefix(h, "~"), strings.HasPrefix(h, "@"):
		return "", false, false
	case strings.HasPrefix(h, "="):
		return strings.TrimSpace(strings.TrimPrefix(h, "=")), true, false
	case strings.HasPrefix(h, "^~"):
		h = strings.TrimSpace(strings.TrimPrefix(h, "^~"))
	}
	h = unquote(h)
	if !strings.HasPrefix(h, "/") || strings.ContainsAny(h, " \t$*?") {
		return "", false, false
	}
	return h, false, true
}

// ParseNginx extracts server blocks from nginx config text.
//
// Location blocks are read as well as the server block itself, because one
// hostname forwarding several paths to several backends is ordinary in both
// hand-written nginx and Nginx Proxy Manager, and reading only the first
// proxy_pass turns such a host into a silently wrong import.
func ParseNginx(text, file string) []NginxSite {
	var out []NginxSite
	for _, loc := range serverBlockRe.FindAllStringIndex(text, -1) {
		block, _ := matchBrace(text, loc[1])
		site := NginxSite{File: file}

		// Locations first, so their bodies can be cut out of the block before
		// the server's own directives are read. Otherwise a location's
		// proxy_pass would look like the server's.
		outer := block
		for {
			m := locationRe.FindStringSubmatchIndex(outer)
			if m == nil {
				break
			}
			// The pattern consumes one character before the keyword so
			// "xlocation" cannot match. Cutting from the keyword itself keeps
			// that character, which may be a brace the next block needs.
			kw := m[0] + strings.Index(outer[m[0]:m[1]], "location")
			head := outer[m[2]:m[3]]
			body, end := matchBrace(outer, m[1])
			path, exact, ok := locationPath(head)
			vars := map[string]string{}
			for _, sm := range setRe.FindAllStringSubmatch(body, -1) {
				vars[sm[1]] = unquote(sm[2])
			}
			up, raw, strip := upstreamOf(body, vars)
			if up == "" {
				up = npmUpstream(vars)
			}
			path = strings.TrimSuffix(path, "/")
			switch {
			case path == "" && ok:
				// The root location is the site's own target, not an extra,
				// and a `root` inside it is the site's static directory.
				if site.Upstream == "" && up != "" {
					site.Upstream = up
					site.RootStrip = strip
				}
				if site.RawUp == "" {
					site.RawUp = raw
				}
				if site.Root == "" {
					for _, rm := range directiveRe.FindAllStringSubmatch(body, -1) {
						if rm[1] == "root" {
							site.Root = unquote(strings.TrimSpace(rm[2]))
							break
						}
					}
				}
			case ok && up != "":
				site.Locations = append(site.Locations, SiteLocation{Path: path, Upstream: up, StripPath: strip})
			case ok && raw != "":
				site.Locations = append(site.Locations, SiteLocation{Path: path, RawUp: raw})
			case !ok && (up != "" || raw != ""):
				// Worth naming: it forwards somewhere, and the person should
				// know this one part of their host did not come across.
				site.Skipped = append(site.Skipped, strings.TrimSpace(head))
			case exact:
				site.Skipped = append(site.Skipped, strings.TrimSpace(head))
			}
			outer = outer[:kw] + outer[end:]
		}

		vars := map[string]string{}
		for _, m := range setRe.FindAllStringSubmatch(outer, -1) {
			vars[m[1]] = unquote(m[2])
		}
		for _, m := range directiveRe.FindAllStringSubmatch(outer, -1) {
			val := unquote(resolveVars(strings.TrimSpace(m[2]), vars))
			switch m[1] {
			case "server_name":
				for _, h := range strings.Fields(val) {
					if h != "_" && !strings.HasPrefix(h, "~") && !strings.Contains(h, "$") {
						site.Hosts = append(site.Hosts, strings.ToLower(h))
					}
				}
			case "include":
				// NPM keeps the real proxy_pass in an included snippet for
				// some host types; note it so the import can say why a host
				// came through without a target.
				if site.Include == "" && strings.Contains(val, "proxy") {
					site.Include = val
				}
			case "root":
				if site.Root == "" {
					site.Root = val
				}
			case "listen":
				if strings.Contains(val, "443") || strings.Contains(val, "ssl") {
					site.TLS = true
				}
			}
		}
		if up, raw, strip := upstreamOf(outer, vars); site.Upstream == "" {
			site.Upstream, site.RootStrip = up, strip
			if site.RawUp == "" {
				site.RawUp = raw
			}
		}
		// Nginx Proxy Manager keeps the proxy_pass itself in an included
		// snippet (conf.d/include/proxy.conf), so the host file only carries
		// the variables. Those are enough: $server and $port are the upstream.
		if site.Upstream == "" {
			site.Upstream = npmUpstream(vars)
		}
		// Nothing answers the root: no forward and no static directory, only
		// paths. Every product here allows that; Islet needs a root target,
		// so the import has to decide what it becomes and say so.
		if site.Upstream == "" && site.Root == "" && len(site.Locations) > 0 {
			site.NoRoot = true
		}
		if len(site.Hosts) > 0 {
			out = append(out, site)
		}
	}
	return out
}

// ReadNginxSites collects sites from the usual locations on Linux.
func ReadNginxSites() []NginxSite {
	if runtime.GOOS != "linux" {
		return nil
	}
	var out []NginxSite
	for _, glob := range []string{"/etc/nginx/sites-enabled/*", "/etc/nginx/conf.d/*.conf"} {
		files, _ := filepath.Glob(glob)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			out = append(out, ParseNginx(string(b), f)...)
		}
	}
	return out
}

// UpstreamParts splits "http://name:8080" into its host and port. ok is
// false for anything that is not a plain scheme://host[:port] upstream.
func UpstreamParts(upstream string) (host string, port int, ok bool) {
	m := upstreamRe.FindStringSubmatch(strings.TrimSpace(upstream))
	if m == nil {
		return "", 0, false
	}
	port = 80
	if m[1] == "https" {
		port = 443
	}
	if m[3] != "" {
		n, err := strconv.Atoi(m[3])
		if err != nil || n < 1 || n > 65535 {
			return "", 0, false
		}
		port = n
	}
	return m[2], port, true
}

var upstreamRe = regexp.MustCompile(`^(https?)://([A-Za-z0-9._-]+)(?::(\d+))?/?$`)
