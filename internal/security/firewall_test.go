package security

import (
	"strings"
	"testing"
)

// join renders a command list the way a human reads it, for assertions.
func join(cmds [][]string) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func hasCmd(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// The bug this guards against: a port published by a container reaches it on
// the forward chain, so an input rule alone leaves every site dark while the
// host keeps answering.
func TestPlanGivesProxyPortsForwardRules(t *testing.T) {
	plan, warn := FirewallPlan(PlanInput{SSHPort: 22, HTTPPort: "80", HTTPSPort: "443", PanelPort: "9443", AdminIP: "203.0.113.9"})
	var cmds []string
	for _, o := range plan {
		if err := o.Validate(); err != nil {
			t.Fatalf("invalid opening %+v: %v", o, err)
		}
		cmds = append(cmds, join(o.Commands())...)
	}
	for _, want := range []string{
		"ufw route allow proto tcp from any to any port 80 comment HTTP routed",
		"ufw route allow proto tcp from any to any port 443 comment HTTPS routed",
		"ufw route allow proto udp from any to any port 443 comment HTTP3 routed",
		"ufw allow 80/tcp comment HTTP",
		"ufw allow 443/tcp comment HTTPS",
		"ufw limit 22/tcp comment SSH",
	} {
		if !hasCmd(cmds, want) {
			t.Errorf("plan is missing %q\ngot:\n%s", want, strings.Join(cmds, "\n"))
		}
	}
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
}

// The panel is the daemon on the host, so it never needs a forward rule, and it
// is not opened to the internet when the admin has another way in.
func TestPlanKeepsThePanelOffTheInternet(t *testing.T) {
	cases := []struct {
		name   string
		in     PlanInput
		want   string
		warn   bool
		absent bool
	}{
		{
			name: "admin address only",
			in:   PlanInput{HTTPPort: "80", HTTPSPort: "443", PanelPort: "9443", AdminIP: "203.0.113.9"},
			want: "ufw allow from 203.0.113.9 to any port 9443 proto tcp comment Islet panel admin",
		},
		{
			name: "an explicit range wins",
			in:   PlanInput{HTTPPort: "80", HTTPSPort: "443", PanelPort: "9443", PanelCIDR: "10.8.0.0/24", AdminIP: "203.0.113.9"},
			want: "ufw allow from 10.8.0.0/24 to any port 9443 proto tcp comment Islet panel",
		},
		{
			name: "asked for public",
			in:   PlanInput{HTTPPort: "80", HTTPSPort: "443", PanelPort: "9443", PanelPublic: true, AdminIP: "203.0.113.9"},
			want: "ufw allow 9443/tcp comment Islet panel",
		},
		{
			name:   "a panel domain means the port can stay shut",
			in:     PlanInput{HTTPPort: "80", HTTPSPort: "443", PanelPort: "9443", PanelDomain: true},
			absent: true,
			warn:   true,
		},
		{
			name: "no domain and no address: opened, loudly",
			in:   PlanInput{HTTPPort: "80", HTTPSPort: "443", PanelPort: "9443"},
			want: "ufw allow 9443/tcp comment Islet panel",
			warn: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, warn := FirewallPlan(tc.in)
			var cmds []string
			for _, o := range plan {
				if o.Routed && o.Port == "9443" {
					t.Error("the panel runs on the host and must never get a forward rule")
				}
				cmds = append(cmds, join(o.Commands())...)
			}
			if tc.absent {
				for _, c := range cmds {
					if strings.Contains(c, "9443") {
						t.Errorf("panel port should be closed, got %q", c)
					}
				}
			} else if !hasCmd(cmds, tc.want) {
				t.Errorf("missing %q\ngot:\n%s", tc.want, strings.Join(cmds, "\n"))
			}
			if tc.warn != (warn != "") {
				t.Errorf("warning = %q, wanted one: %v", warn, tc.warn)
			}
		})
	}
}

// A dev box moves the proxy to 8880 and 8443, and SSH may have been moved too.
func TestPlanFollowsTheRealPorts(t *testing.T) {
	plan, _ := FirewallPlan(PlanInput{SSHPort: 2222, HTTPPort: "8880", HTTPSPort: "8443", PanelPort: "9443", AdminIP: "203.0.113.9"})
	var cmds []string
	for _, o := range plan {
		cmds = append(cmds, join(o.Commands())...)
	}
	for _, want := range []string{
		"ufw limit 2222/tcp comment SSH",
		"ufw route allow proto tcp from any to any port 8880 comment HTTP routed",
		"ufw route allow proto tcp from any to any port 8443 comment HTTPS routed",
	} {
		if !hasCmd(cmds, want) {
			t.Errorf("missing %q\ngot:\n%s", want, strings.Join(cmds, "\n"))
		}
	}
	for _, unwanted := range []string{"ufw allow 80/tcp comment HTTP", "ufw limit 22/tcp comment SSH"} {
		if hasCmd(cmds, unwanted) {
			t.Errorf("should not open a port nothing listens on: %q", unwanted)
		}
	}
}

func TestOpeningValidate(t *testing.T) {
	bad := []Opening{
		{Port: "notaport", Proto: "tcp", Host: true},
		{Port: "80", Proto: "sctp", Host: true},
		{Port: "80", Proto: "tcp", Host: true, From: "1.2.3.4; rm -rf /"},
		{Port: "80", Proto: "tcp"}, // neither chain
	}
	for _, o := range bad {
		if err := o.Validate(); err == nil {
			t.Errorf("%+v should be rejected", o)
		}
	}
	for _, o := range []Opening{
		{Port: "80", Proto: "tcp", Host: true},
		{Port: "8000:8010", Proto: "udp", Routed: true},
		{Port: "443", Proto: "tcp", Host: true, Routed: true, From: "10.0.0.0/8"},
	} {
		if err := o.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", o, err)
		}
	}
}

func TestDeleteUndoesWhatAllowCreated(t *testing.T) {
	o := Opening{Port: "5432", Proto: "tcp", Host: true, Routed: true, From: "203.0.113.0/24", Comment: "db pg"}
	got := join(o.DeleteCommands())
	for _, want := range []string{
		"ufw delete allow from 203.0.113.0/24 to any port 5432 proto tcp comment db pg",
		"ufw route delete allow proto tcp from 203.0.113.0/24 to any port 5432 comment db pg routed",
	} {
		if !hasCmd(got, want) {
			t.Errorf("missing %q\ngot:\n%s", want, strings.Join(got, "\n"))
		}
	}
}

// Real ufw output, including the forward rules that the old parser folded in
// with the input rules.
const ufwStatus = `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     LIMIT       Anywhere                   # SSH
80/tcp                     ALLOW       Anywhere                   # HTTP
443/tcp                    ALLOW       Anywhere                   # HTTPS
9443/tcp                   ALLOW       203.0.113.9                # Islet panel admin
80/tcp                     ALLOW FWD   Anywhere                   # HTTP routed
443/tcp                    ALLOW FWD   Anywhere                   # HTTPS routed
22/tcp (v6)                LIMIT       Anywhere (v6)              # SSH
80/tcp (v6)                ALLOW       Anywhere (v6)              # HTTP
`

func TestParseUFWStatus(t *testing.T) {
	active, rules := ParseUFWStatus(ufwStatus)
	if !active {
		t.Fatal("status says active")
	}
	if len(rules) != 6 {
		t.Fatalf("want 6 rules without the v6 twins, got %d: %+v", len(rules), rules)
	}
	var routed, input int
	for _, r := range rules {
		if r.Routed {
			routed++
		} else {
			input++
		}
	}
	if routed != 2 || input != 4 {
		t.Errorf("want 2 forward and 4 input rules, got %d and %d", routed, input)
	}
	if rules[0].Port != "22" || rules[0].Proto != "tcp" || rules[0].Comment != "SSH" || rules[0].From != "Anywhere" {
		t.Errorf("first rule parsed as %+v", rules[0])
	}
	if rules[3].From != "203.0.113.9" {
		t.Errorf("restricted rule parsed as %+v", rules[3])
	}
}

func TestParseUFWStatusInactiveAndNumbered(t *testing.T) {
	if active, _ := ParseUFWStatus("Status: inactive\n"); active {
		t.Error("inactive read as active")
	}
	_, rules := ParseUFWStatus("Status: active\n\n     To                         Action      From\n     --                         ------      ----\n[ 1] 80/tcp                     ALLOW IN    Anywhere\n[ 2] 443/tcp                    ALLOW FWD   Anywhere\n")
	if len(rules) != 2 {
		t.Fatalf("numbered listing gave %+v", rules)
	}
	if rules[0].Routed || !rules[1].Routed {
		t.Errorf("chains read wrong: %+v", rules)
	}
}

// The check that would have caught the outage.
func TestMissingRoutes(t *testing.T) {
	_, rules := ParseUFWStatus(`Status: active

To                         Action      From
--                         ------      ----
80/tcp                     ALLOW       Anywhere
443/tcp                    ALLOW       Anywhere
`)
	fw := Firewall{Active: true, DockerOK: true, Rules: rules}
	missing := MissingRoutes(fw, "80", "443")
	if len(missing) != 2 {
		t.Fatalf("both proxy ports are unreachable, got %v", missing)
	}

	_, ok := ParseUFWStatus(ufwStatus)
	fw = Firewall{Active: true, DockerOK: true, Rules: ok}
	if m := MissingRoutes(fw, "80", "443"); len(m) != 0 {
		t.Errorf("nothing should be missing, got %v", m)
	}

	// Without the Docker rules there is nothing to route around.
	fw = Firewall{Active: true, DockerOK: false, Rules: rules}
	if m := MissingRoutes(fw, "80", "443"); len(m) != 0 {
		t.Errorf("no Docker rules means no forward requirement, got %v", m)
	}
}

// The proxy dials the daemon for forward auth and the panel route. Closing the
// panel port to the internet must not close it to the proxy, or every protected
// domain answers 502.
func TestPlanLetsTheProxyReachThePanel(t *testing.T) {
	plan, _ := FirewallPlan(PlanInput{
		HTTPPort: "80", HTTPSPort: "443", PanelPort: "9443",
		PanelDomain: true, ProxySubnet: "172.20.0.0/16",
	})
	var cmds []string
	for _, o := range plan {
		cmds = append(cmds, join(o.Commands())...)
	}
	want := "ufw allow from 172.20.0.0/16 to any port 9443 proto tcp comment Islet panel from the proxy"
	if !hasCmd(cmds, want) {
		t.Errorf("missing %q\ngot:\n%s", want, strings.Join(cmds, "\n"))
	}
	if hasCmd(cmds, "ufw allow 9443/tcp comment Islet panel") {
		t.Error("the panel port should still be closed to the internet")
	}
}
