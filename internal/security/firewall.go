package security

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A port on a Docker host arrives on one of two chains, and ufw treats them
// differently. A port that a process on the host listens on is INPUT, which is
// what `ufw allow` writes. A port published by a container is FORWARDed to it,
// which needs `ufw route allow`. With the ufw-docker rules installed and
// forwarding denied by default, `ufw allow 443/tcp` alone leaves every
// container unreachable while the host itself still answers.
//
// Everything here exists so the rules are derived from what Islet actually
// runs, rather than a hardcoded list that drifts from reality.

// Opening is one port the firewall must let in, and the chains it arrives on.
type Opening struct {
	Port    string // a number, or a range like "8000:8010"
	Proto   string // tcp or udp
	Host    bool   // something on the host listens: needs an input rule
	Routed  bool   // a container publishes it: needs a route rule
	From    string // empty means anywhere; otherwise an address or CIDR
	Limit   bool   // rate limit new connections instead of a plain allow
	Comment string
}

// PlanInput is what this server actually needs open.
type PlanInput struct {
	SSHPort     int    // from sshd_config, so a moved port is not locked out
	HTTPPort    string // the proxy's published HTTP port, usually 80
	HTTPSPort   string // the proxy's published HTTPS port, usually 443
	PanelPort   string // the port isletd itself listens on
	PanelCIDR   string // an explicit range the panel is restricted to
	PanelPublic bool   // open the panel port to everyone
	PanelDomain bool   // the panel is reachable through the proxy as well
	AdminIP     string // the address of the admin asking for this
}

// FirewallPlan lists every opening this server needs, and a warning when the
// plan had to keep something open that the caller would rather have closed.
func FirewallPlan(in PlanInput) ([]Opening, string) {
	ssh := in.SSHPort
	if ssh <= 0 {
		ssh = 22
	}
	http, https := in.HTTPPort, in.HTTPSPort
	if http == "" {
		http = "80"
	}
	if https == "" {
		https = "443"
	}

	out := []Opening{
		{Port: strconv.Itoa(ssh), Proto: "tcp", Host: true, Limit: true, Comment: "SSH"},
		// The proxy is a container, so these need route rules. The input rules
		// stay as well: they cost nothing and they keep the ports working if
		// the ufw-docker rules are ever removed.
		{Port: http, Proto: "tcp", Host: true, Routed: true, Comment: "HTTP"},
		{Port: https, Proto: "tcp", Host: true, Routed: true, Comment: "HTTPS"},
		{Port: https, Proto: "udp", Host: true, Routed: true, Comment: "HTTP3"},
	}

	// The panel is the daemon itself, on the host, so it never needs a route
	// rule. How far it is opened is the one real choice here.
	panel := in.PanelPort
	if panel == "" {
		panel = "9443"
	}
	warn := ""
	switch {
	case in.PanelPublic:
		out = append(out, Opening{Port: panel, Proto: "tcp", Host: true, Comment: "Islet panel"})
	case in.PanelCIDR != "":
		out = append(out, Opening{Port: panel, Proto: "tcp", Host: true, From: in.PanelCIDR, Comment: "Islet panel"})
	case in.AdminIP != "":
		out = append(out, Opening{Port: panel, Proto: "tcp", Host: true, From: in.AdminIP, Comment: "Islet panel admin"})
	case in.PanelDomain:
		// Closed on purpose: the panel answers on its domain through the proxy.
		warn = "the panel port " + panel + " is closed to the internet; reach the panel on its domain, or over SSH"
	default:
		out = append(out, Opening{Port: panel, Proto: "tcp", Host: true, Comment: "Islet panel"})
		warn = "the panel port " + panel + " is open to everyone: there is no panel domain and your address is not known, so closing it would lock you out. Route a domain to the panel, then run this again."
	}
	return out, warn
}

var portRe = regexp.MustCompile(`^\d{1,5}(:\d{1,5})?$`)
var fromRe = regexp.MustCompile(`^[0-9a-fA-F.:/]+$`)
var commentRe = regexp.MustCompile(`[^A-Za-z0-9 ._-]`)

// Validate rejects anything that should never reach the ufw command line.
func (o Opening) Validate() error {
	if !portRe.MatchString(o.Port) {
		return fmt.Errorf("port %q must be a number or a range", o.Port)
	}
	if o.Proto != "tcp" && o.Proto != "udp" && o.Proto != "" {
		return fmt.Errorf("proto %q must be tcp or udp", o.Proto)
	}
	if o.From != "" && o.From != "any" && !fromRe.MatchString(o.From) {
		return fmt.Errorf("from %q must be an address or CIDR", o.From)
	}
	if !o.Host && !o.Routed {
		return fmt.Errorf("opening %s/%s reaches neither the host nor a container", o.Port, o.Proto)
	}
	return nil
}

// Commands renders the ufw invocations for one opening: the input rule, the
// route rule, or both.
func (o Opening) Commands() [][]string {
	var cmds [][]string
	from := o.From
	if from == "any" {
		from = ""
	}
	comment := commentRe.ReplaceAllString(o.Comment, "")

	if o.Host {
		verb := "allow"
		if o.Limit {
			verb = "limit"
		}
		var args []string
		if from != "" {
			args = []string{verb, "from", from, "to", "any", "port", o.Port}
			if o.Proto != "" {
				args = append(args, "proto", o.Proto)
			}
		} else {
			spec := o.Port
			if o.Proto != "" {
				spec += "/" + o.Proto
			}
			args = []string{verb, spec}
		}
		if comment != "" {
			args = append(args, "comment", comment)
		}
		cmds = append(cmds, append([]string{"ufw"}, args...))
	}

	if o.Routed {
		src := "any"
		if from != "" {
			src = from
		}
		args := []string{"route", "allow"}
		if o.Proto != "" {
			args = append(args, "proto", o.Proto)
		}
		args = append(args, "from", src, "to", "any", "port", o.Port)
		if comment != "" {
			args = append(args, "comment", comment+" routed")
		}
		cmds = append(cmds, append([]string{"ufw"}, args...))
	}
	return cmds
}

// DeleteCommands undoes what Commands created.
func (o Opening) DeleteCommands() [][]string {
	var out [][]string
	for _, c := range o.Commands() {
		// "ufw delete <rule>" and "ufw route delete <rule>".
		if len(c) > 1 && c[1] == "route" {
			out = append(out, append([]string{"ufw", "route", "delete"}, c[2:]...))
			continue
		}
		out = append(out, append([]string{"ufw", "delete"}, c[1:]...))
	}
	return out
}

var actions = map[string]bool{"ALLOW": true, "DENY": true, "LIMIT": true, "REJECT": true}

// ParseUFWStatus reads `ufw status`. Route rules show as "ALLOW FWD" and are
// the ones that matter for published container ports, so they are kept apart
// from input rules rather than folded in with them.
func ParseUFWStatus(out string) (active bool, rules []FirewallRule) {
	rules = []FirewallRule{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "Status:") {
			// "inactive" contains "active", so compare the word itself.
			active = strings.TrimSpace(strings.TrimPrefix(t, "Status:")) == "active"
			continue
		}
		body, comment := line, ""
		if i := strings.Index(line, "#"); i >= 0 {
			body, comment = line[:i], strings.TrimSpace(line[i+1:])
		}
		// A numbered listing prefixes each rule with "[ 1] ".
		body = regexp.MustCompile(`^\s*\[\s*\d+\]\s*`).ReplaceAllString(body, "")
		if strings.Contains(body, "(v6)") {
			continue // the v6 twin of a rule we already have
		}
		f := strings.Fields(body)
		if len(f) < 3 {
			continue
		}
		at := -1
		for i, w := range f {
			if actions[w] {
				at = i
				break
			}
		}
		if at < 1 {
			continue
		}
		action := f[at]
		routed := at+1 < len(f) && f[at+1] == "FWD"
		rest := f[at+1:]
		if routed || (at+1 < len(f) && (f[at+1] == "IN" || f[at+1] == "OUT")) {
			rest = f[at+2:]
		}
		if action != "ALLOW" && action != "LIMIT" {
			continue
		}
		to := strings.Join(f[:at], " ")
		port, proto, _ := strings.Cut(to, "/")
		rules = append(rules, FirewallRule{
			Port:    port,
			Proto:   proto,
			From:    strings.Join(rest, " "),
			Routed:  routed,
			Comment: comment,
		})
	}
	return active, rules
}

// MissingRoutes reports the proxy ports that have no route rule while the
// ufw-docker rules are in place. Each one is a site that answers nothing.
func MissingRoutes(fw Firewall, httpPort, httpsPort string) []string {
	if !fw.Active || !fw.DockerOK {
		return nil
	}
	if httpPort == "" {
		httpPort = "80"
	}
	if httpsPort == "" {
		httpsPort = "443"
	}
	have := map[string]bool{}
	for _, r := range fw.Rules {
		if r.Routed {
			have[r.Port] = true
		}
	}
	var missing []string
	for _, p := range []string{httpPort, httpsPort} {
		if !have[p] && !contains(missing, p) {
			missing = append(missing, p)
		}
	}
	return missing
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
