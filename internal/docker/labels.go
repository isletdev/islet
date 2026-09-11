package docker

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Route is a domain a Compose stack already serves through Traefik labels
// (Coolify, Dokploy and hand-written stacks all use them).
type Route struct {
	Service string `json:"service"`
	Host    string `json:"host"`
	Prefix  string `json:"prefix,omitempty"`
	Port    int    `json:"port"`
	TLS     bool   `json:"tls"`
}

var (
	hostRe   = regexp.MustCompile("Host\\(`([^`]+)`\\)")
	prefixRe = regexp.MustCompile("PathPrefix\\(`([^`]+)`\\)")
)

// ExtractRoutes reads Traefik router and service labels from a Compose file
// and returns one route per host. Labels may be a list ("k=v") or a map.
func ExtractRoutes(compose string) []Route {
	var doc struct {
		Services map[string]struct {
			Labels any      `yaml:"labels"`
			Expose []any    `yaml:"expose"`
			Ports  []any    `yaml:"ports"`
			Image  string   `yaml:"image"`
			Deploy struct{} `yaml:"deploy"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(compose), &doc) != nil {
		return nil
	}
	var out []Route
	names := make([]string, 0, len(doc.Services))
	for n := range doc.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := doc.Services[name]
		labels := labelMap(svc.Labels)
		if len(labels) == 0 {
			continue
		}
		// One port for the whole service: an explicit loadbalancer port, else
		// the first exposed/published container port.
		port := 0
		for k, v := range labels {
			if strings.HasPrefix(k, "traefik.http.services.") && strings.HasSuffix(k, ".loadbalancer.server.port") {
				port, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if port == 0 {
			port = firstPort(svc.Expose, svc.Ports)
		}
		if port == 0 {
			port = 80
		}
		seen := map[string]bool{}
		routers := make([]string, 0)
		for k := range labels {
			if strings.HasPrefix(k, "traefik.http.routers.") && strings.HasSuffix(k, ".rule") {
				routers = append(routers, k)
			}
		}
		sort.Strings(routers)
		for _, k := range routers {
			rule := labels[k]
			router := strings.TrimSuffix(strings.TrimPrefix(k, "traefik.http.routers."), ".rule")
			tls := labels["traefik.http.routers."+router+".tls.certresolver"] != "" || labels["traefik.http.routers."+router+".tls"] == "true"
			prefix := ""
			if m := prefixRe.FindStringSubmatch(rule); m != nil && m[1] != "/" {
				prefix = strings.TrimSuffix(m[1], "/")
			}
			for _, m := range hostRe.FindAllStringSubmatch(rule, -1) {
				for _, h := range strings.Split(m[1], ",") {
					h = strings.ToLower(strings.TrimSpace(strings.Trim(h, "`")))
					if h == "" || seen[h+prefix] {
						continue
					}
					seen[h+prefix] = true
					out = append(out, Route{Service: name, Host: h, Prefix: prefix, Port: port, TLS: tls})
				}
			}
		}
	}
	return out
}

func labelMap(v any) map[string]string {
	out := map[string]string{}
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			out[k] = strings.TrimSpace(stringify(val))
		}
	case []any:
		for _, item := range x {
			k, val, ok := strings.Cut(stringify(item), "=")
			if ok {
				out[strings.TrimSpace(k)] = strings.TrimSpace(val)
			}
		}
	}
	return out
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func firstPort(expose, ports []any) int {
	for _, e := range expose {
		if p, err := strconv.Atoi(strings.Split(stringify(e), "/")[0]); err == nil {
			return p
		}
	}
	for _, p := range ports {
		s := stringify(p)
		if s == "" {
			if m, ok := p.(map[string]any); ok {
				s = stringify(m["target"])
			}
		}
		s = strings.Split(s, "/")[0]
		if i := strings.LastIndex(s, ":"); i >= 0 {
			s = s[i+1:]
		}
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 0
}
