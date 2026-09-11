package proxy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// NginxSite is one server block found in an nginx configuration.
type NginxSite struct {
	Hosts    []string `json:"hosts"`
	Upstream string   `json:"upstream"` // proxy_pass target, when any
	Root     string   `json:"root"`     // static root, when any
	TLS      bool     `json:"tls"`
	File     string   `json:"file"`
}

var (
	serverBlockRe = regexp.MustCompile(`(?s)server\s*\{`)
	directiveRe   = regexp.MustCompile(`(?:^|[\s{;])(server_name|proxy_pass|root|listen)\s+([^;{}]+);`)
)

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
		for _, m := range directiveRe.FindAllStringSubmatch(block, -1) {
			val := strings.TrimSpace(m[2])
			switch m[1] {
			case "server_name":
				for _, h := range strings.Fields(val) {
					if h != "_" && !strings.HasPrefix(h, "~") && !strings.Contains(h, "$") {
						site.Hosts = append(site.Hosts, strings.ToLower(h))
					}
				}
			case "proxy_pass":
				if site.Upstream == "" {
					site.Upstream = strings.TrimSuffix(val, "/")
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
