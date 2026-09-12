package proxy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// NginxSite is one server block found in an nginx configuration.
type NginxSite struct {
	Hosts    []string `json:"hosts"`
	Upstream string   `json:"upstream"` // proxy_pass target, when any
	Root     string   `json:"root"`     // static root, when any
	Include  string   `json:"include,omitempty"`
	RawUp    string   `json:"rawUpstream,omitempty"` // proxy_pass that still held a variable
	TLS      bool     `json:"tls"`
	File     string   `json:"file"`
}

var (
	serverBlockRe = regexp.MustCompile(`(?s)server\s*\{`)
	directiveRe   = regexp.MustCompile(`(?:^|[\s{;])(server_name|proxy_pass|root|listen|include)\s+([^;{}]+);`)
	// Nginx Proxy Manager writes `set $server "app";` and then
	// `proxy_pass $forward_scheme://$server:$port;`, so the variables have to
	// be resolved before the upstream means anything.
	setRe = regexp.MustCompile(`(?:^|[\s{;])set\s+\$([A-Za-z0-9_]+)\s+([^;{}]+);`)
	varRe = regexp.MustCompile(`\$\{?([A-Za-z0-9_]+)\}?`)
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

// ParseNginx extracts server blocks from nginx config text. Nested braces
// are tracked by hand; only the four directives that matter are read.
func ParseNginx(text, file string) []NginxSite {
	var out []NginxSite
	for _, loc := range serverBlockRe.FindAllStringIndex(text, -1) {
		depth, i := 1, loc[1]
		for i < len(text) && depth > 0 {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
			}
			i++
		}
		block := text[loc[1]:i]
		site := NginxSite{File: file}
		vars := map[string]string{}
		for _, m := range setRe.FindAllStringSubmatch(block, -1) {
			vars[m[1]] = unquote(m[2])
		}
		for _, m := range directiveRe.FindAllStringSubmatch(block, -1) {
			val := unquote(resolveVars(strings.TrimSpace(m[2]), vars))
			switch m[1] {
			case "server_name":
				for _, h := range strings.Fields(val) {
					if h != "_" && !strings.HasPrefix(h, "~") && !strings.Contains(h, "$") {
						site.Hosts = append(site.Hosts, strings.ToLower(h))
					}
				}
			case "proxy_pass":
				// A value still holding a variable cannot be pointed at.
				if strings.Contains(val, "$") {
					if site.RawUp == "" {
						site.RawUp = val
					}
				} else if site.Upstream == "" {
					site.Upstream = strings.TrimSuffix(val, "/")
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
