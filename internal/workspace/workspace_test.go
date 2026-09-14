package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The session name is derived from the id, not the name.
//
// Renaming a workspace must not strand the session its work is running in, and
// a name is a person's text — it can hold characters tmux would read as part of
// a target pattern.
func TestSessionName(t *testing.T) {
	if got := SessionName("a1b2c3"); got != "islet-ws-a1b2c3" {
		t.Errorf("SessionName = %q", got)
	}
	argv := AttachArgv("a1b2c3")
	want := []string{"tmux", "attach-session", "-d", "-t", "islet-ws-a1b2c3"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v", argv)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv = %v, want %v", argv, want)
		}
	}
	// -d is load-bearing: tmux sizes a session to its smallest attached
	// client, so a forgotten tab would otherwise squeeze every other viewer.
	if argv[2] != "-d" {
		t.Error("attach must detach other clients")
	}
}

func TestValidate(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := writeFile(file); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		in   Workspace
		ok   bool
		want func(w Workspace) string // "" when fine
	}{
		{"a claude workspace defaults its command",
			Workspace{Name: "poolse", Directory: dir, Preset: "claude"}, true,
			func(w Workspace) string {
				if w.Command != "claude" {
					return "command = " + w.Command
				}
				return ""
			}},
		{"a shell has no command and no Islet access",
			Workspace{Name: "build", Directory: dir, Preset: "shell", Command: "whatever", MCPEnabled: true}, true,
			func(w Workspace) string {
				if w.Command != "" {
					return "a shell kept a command: " + w.Command
				}
				if w.MCPEnabled {
					return "a shell was given Islet access, but nothing is launched to use it"
				}
				return ""
			}},
		{"custom needs a command", Workspace{Name: "x", Directory: dir, Preset: "custom"}, false, nil},
		{"the name is normalised", Workspace{Name: "  PoolSE  ", Directory: dir, Preset: "claude"}, true,
			func(w Workspace) string {
				if w.Name != "poolse" {
					return "name = " + w.Name
				}
				return ""
			}},
		{"a name with spaces is refused", Workspace{Name: "my project", Directory: dir, Preset: "claude"}, false, nil},
		{"a name that starts with a dash is refused", Workspace{Name: "-x", Directory: dir, Preset: "claude"}, false, nil},
		{"an empty name is refused", Workspace{Name: "", Directory: dir, Preset: "claude"}, false, nil},
		{"a relative directory is refused", Workspace{Name: "x", Directory: "server", Preset: "claude"}, false, nil},
		{"a directory that is a file is refused", Workspace{Name: "x", Directory: file, Preset: "claude"}, false, nil},
		{"a directory that is not there is refused",
			Workspace{Name: "x", Directory: filepath.Join(dir, "nope"), Preset: "claude"}, false, nil},
		{"an unknown preset is refused", Workspace{Name: "x", Directory: dir, Preset: "magic"}, false, nil},
		// The command is typed into the session with send-keys. A newline in it
		// would run whatever came after as a second command.
		{"a command with a newline is refused",
			Workspace{Name: "x", Directory: dir, Preset: "custom", Command: "echo hi\nrm -rf /"}, false, nil},
	} {
		w := tc.in
		err := w.Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("%s: accepted %+v", tc.name, tc.in)
			}
			continue
		}
		if tc.want != nil {
			if msg := tc.want(w); msg != "" {
				t.Errorf("%s: %s", tc.name, msg)
			}
		}
	}
}

// The MCP credential must never be written inside the project. Claude Code also
// reads .mcp.json from a repository root, and a bearer token there is one
// `git add .` away from being published.
func TestMCPLivesOutsideTheProject(t *testing.T) {
	data := t.TempDir()
	project := t.TempDir()
	s := New(nil, nil, nil, data, nil)

	if err := s.WriteMCP("abc123", "https://panel.example.com", "islet_secret_value"); err != nil {
		t.Fatal(err)
	}
	p := s.MCPPath("abc123")
	if !strings.HasPrefix(p, data) {
		t.Errorf("the credential is at %q, outside the data directory", p)
	}
	if strings.HasPrefix(p, project) {
		t.Error("the credential was written into the project")
	}
	body, err := readFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"islet_secret_value", "https://panel.example.com/mcp", `"type":"http"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the config is missing %q:\n%s", want, body)
		}
	}
	if runtime.GOOS == "windows" {
		return // file modes are not enforced here; the server is Linux
	}
	if mode, err := fileMode(p); err != nil {
		t.Fatal(err)
	} else if mode.Perm()&0o077 != 0 {
		t.Errorf("mode is %v; a token readable by anyone else is a leaked token", mode.Perm())
	}
}

// Small helpers so the tests read as tests rather than as file plumbing.

func writeFile(p string) error { return os.WriteFile(p, []byte("x"), 0o600) }

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

func fileMode(p string) (fs.FileMode, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.Mode(), nil
}
