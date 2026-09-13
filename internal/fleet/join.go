package fleet

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Joining a server, step by step.
//
// The shape of this matters more than any single step: everything is done over
// one SSH connection, nothing is left behind that the person did not ask for,
// and every step reports what it is doing so the wizard can show it rather than
// spinning. A join that fails half way leaves a server that still works; it
// just is not managed.

// Step names, in order. The wizard draws these.
var Steps = []string{"connect", "check", "key", "install", "token", "verify"}

// Progress is one line of the join, for the wizard.
type Progress struct {
	Step string `json:"step"`
	Line string `json:"line"`
	Done bool   `json:"done,omitempty"`
	Err  string `json:"error,omitempty"`
}

// Result is what the controller keeps once a join succeeds.
type Result struct {
	HostKey  string
	Token    string
	Version  string
	Hostname string
}

// releaseChannel is where a managed server gets its daemon from. It follows the
// controller, so a fleet does not drift into a dozen versions.
const installURL = "https://get.islet.dev"

var versionRe = regexp.MustCompile(`islet(?:d)?\s+v?([0-9][^\s(]*)`)

// Join installs the daemon on a server and returns what is needed to manage it.
//
// version is the controller's own version, so the new server starts on the same
// one. A caller that passes "" gets the latest release.
func Join(ctx context.Context, addr string, creds Credentials, pub *Keypair, version string, say func(Progress)) (*Result, error) {
	step := func(name, line string) { say(Progress{Step: name, Line: line}) }
	fail := func(name string, err error) (*Result, error) {
		say(Progress{Step: name, Err: err.Error()})
		return nil, err
	}

	step("connect", "Connecting to "+addr+" as "+creds.User+"…")
	conn, err := Dial(ctx, addr, creds, "")
	if err != nil {
		return fail("connect", err)
	}
	defer conn.Close()
	step("connect", "Connected. Host key recorded.")

	// ---- what are we looking at ----
	step("check", "Checking the server…")
	out, err := conn.Run(ctx, "id -u; uname -s; uname -m; . /etc/os-release 2>/dev/null && echo \"$ID $VERSION_ID\"")
	if err != nil {
		return fail("check", fmt.Errorf("could not read the server's details: %w", err))
	}
	lines := splitLines(out)
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "0" {
		return fail("check", errors.New("that account is not root on the server. Islet manages Docker, the firewall and system services, so it needs root"))
	}
	if !strings.EqualFold(strings.TrimSpace(lines[1]), "linux") {
		return fail("check", errors.New("Islet manages Linux servers; this one reports "+strings.TrimSpace(lines[1])))
	}
	if len(lines) > 3 {
		step("check", "Found "+strings.TrimSpace(lines[3])+" on "+strings.TrimSpace(lines[2])+".")
	}

	// An existing install is fine, and common: someone installs by hand and
	// then decides to manage it from here.
	existing, _ := conn.Run(ctx, "command -v isletd >/dev/null 2>&1 && isletd -version || true")
	already := strings.Contains(existing, "islet")
	if already {
		step("check", "Islet is already installed here: "+strings.TrimSpace(existing))
	}

	// ---- our own key, so the password is never needed again ----
	step("key", "Installing this panel's key…")
	if err := installKey(ctx, conn, pub.PublicLine); err != nil {
		return fail("key", err)
	}
	step("key", "Key installed. The credentials you gave are not stored.")

	// ---- the daemon ----
	if already {
		step("install", "Skipping the install; the daemon is already here.")
	} else {
		step("install", "Installing the daemon. This pulls Docker if it is missing, so it can take a few minutes.")
		cmd := "curl -fsSL " + installURL + " | sh"
		if version != "" {
			cmd = "curl -fsSL " + installURL + " | ISLET_VERSION=v" + strings.TrimPrefix(version, "v") + " sh"
		}
		if err := conn.Stream(ctx, cmd, func(l string) {
			if l = strings.TrimSpace(l); l != "" {
				say(Progress{Step: "install", Line: l})
			}
		}); err != nil {
			return fail("install", fmt.Errorf("the install did not finish: %w", err))
		}
	}

	// ---- a token for this controller ----
	step("token", "Asking the server for a token…")
	tok, err := conn.Run(ctx, "isletd fleet-token --name 'managed by "+hostLabel(addr)+"'")
	if err != nil {
		return fail("token", fmt.Errorf("could not get a token: %w: %s", err, lastLine(tok)))
	}
	token := strings.TrimSpace(lastLine(tok))
	if !strings.HasPrefix(token, "islet_") {
		return fail("token", errors.New("the server did not return a usable token: "+strings.TrimSpace(tok)))
	}
	step("token", "Token received.")

	// ---- prove it works the way we will use it from now on ----
	step("verify", "Checking the panel answers through the tunnel…")
	res := &Result{HostKey: conn.HostKey, Token: token}
	hostname, _ := conn.Run(ctx, "hostname")
	res.Hostname = strings.TrimSpace(hostname)
	ver, _ := conn.Run(ctx, "isletd -version")
	if m := versionRe.FindStringSubmatch(ver); m != nil {
		res.Version = m[1]
	}
	step("verify", "Ready."+versionNote(res.Version, version))
	say(Progress{Step: "verify", Done: true})
	return res, nil
}

// installKey appends the controller's public key to root's authorized_keys,
// without disturbing what is already there and without adding it twice.
func installKey(ctx context.Context, c *Conn, line string) error {
	// Single-quoted so nothing in the key is interpreted; keys are base64 and a
	// comment we generated, so there is no quote to escape, but be explicit.
	if strings.Contains(line, "'") {
		return errors.New("refusing to install a key containing a quote")
	}
	script := strings.Join([]string{
		"set -e",
		"mkdir -p ~/.ssh",
		"chmod 700 ~/.ssh",
		"touch ~/.ssh/authorized_keys",
		"chmod 600 ~/.ssh/authorized_keys",
		"grep -qxF '" + line + "' ~/.ssh/authorized_keys || echo '" + line + "' >> ~/.ssh/authorized_keys",
	}, "; ")
	out, err := c.Run(ctx, script)
	if err != nil {
		return fmt.Errorf("could not install the key: %w: %s", err, lastLine(out))
	}
	return nil
}

func versionNote(remote, local string) string {
	remote, local = strings.TrimPrefix(remote, "v"), strings.TrimPrefix(local, "v")
	if remote == "" || local == "" || remote == local {
		return ""
	}
	return " This server is on " + remote + " and the panel is on " + local + "; update it from the servers list."
}

func hostLabel(addr string) string {
	if h, _, ok := strings.Cut(addr, ":"); ok {
		return h
	}
	return addr
}

func splitLines(s string) []string { return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") }

func lastLine(s string) string {
	l := splitLines(strings.TrimSpace(s))
	if len(l) == 0 {
		return ""
	}
	return l[len(l)-1]
}
