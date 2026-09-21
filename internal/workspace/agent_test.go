package workspace

import (
	"strings"
	"testing"
)

// Resuming has to be exact, and the obvious way to do it is wrong.
//
// `claude --continue` reopens the most recent conversation *for a directory*.
// Agents in one workspace share a directory, so two of them resuming would both
// land in whichever conversation was touched last, and the other would be lost
// with no error anywhere. Pinning each agent to its own session id is the whole
// reason the field exists.
func TestLaunchAgentResume(t *testing.T) {
	s := &Service{}
	w := &Workspace{ID: "ws1", Directory: "/srv/app"}
	uuid := "11111111-2222-4333-8444-555555555555"

	first := &Agent{Preset: "claude", Command: "/usr/bin/claude", Resume: true, SessionUUID: uuid}
	if got, want := s.launchAgent(nil, w, first), "/usr/bin/claude --session-id "+uuid; got != want {
		t.Errorf("first run = %q, want %q", got, want)
	}

	again := &Agent{Preset: "claude", Command: "/usr/bin/claude", Resume: true, SessionUUID: uuid, LastStarted: "2026-09-14T10:00:00Z"}
	if got, want := s.launchAgent(nil, w, again), "/usr/bin/claude --resume "+uuid; got != want {
		t.Errorf("second run = %q, want %q", got, want)
	}

	// --continue must never appear: it is the flag that cannot tell two agents
	// in one directory apart.
	for _, a := range []*Agent{first, again} {
		if got := s.launchAgent(nil, w, a); contains(got, "--continue") {
			t.Errorf("launch used --continue: %q", got)
		}
	}

	off := &Agent{Preset: "claude", Command: "/usr/bin/claude", Resume: false, SessionUUID: uuid, LastStarted: "2026-09-14T10:00:00Z"}
	if got, want := s.launchAgent(nil, w, off), "/usr/bin/claude"; got != want {
		t.Errorf("resume off = %q, want %q", got, want)
	}

	skip := &Agent{Preset: "claude", Command: "/usr/bin/claude", SkipPermissions: true}
	if got, want := s.launchAgent(nil, w, skip), "/usr/bin/claude --dangerously-skip-permissions"; got != want {
		t.Errorf("skip = %q, want %q", got, want)
	}
}

// Two agents never share a conversation, however they were created.
func TestAgentValidateGivesEachItsOwnSession(t *testing.T) {
	a := &Agent{Name: "worker", Preset: "claude"}
	b := &Agent{Name: "tester", Preset: "claude"}
	for _, x := range []*Agent{a, b} {
		if err := x.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	}
	if a.SessionUUID == "" || b.SessionUUID == "" {
		t.Fatal("a Claude agent must be given a conversation of its own")
	}
	if a.SessionUUID == b.SessionUUID {
		t.Fatal("two agents were given the same conversation")
	}
	if len(a.SessionUUID) != 36 {
		t.Errorf("session id is not a uuid: %q", a.SessionUUID)
	}

	// The shell window belongs to the workspace, so an agent cannot take its
	// name and quietly become the thing you type `git status` into.
	if err := (&Agent{Name: ShellWindow, Preset: "shell"}).Validate(); err == nil {
		t.Error("an agent was allowed to be named after the workspace's own shell window")
	}

	// A shell agent has nothing to resume, but saying so is the command's job
	// rather than the preset's: what the flags do is decided when the line is
	// built, and a preset that erased them made `claude --model opus-5` under
	// "something else" unable to resume its own conversation.
	sh := &Agent{Name: "runner", Preset: "shell", Command: "", Resume: true, SkipPermissions: true}
	if err := sh.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := resolveAgentCommand("htop", "", "/etc/islet/mcp.json", sh); got != "htop" {
		t.Errorf("a command that is not Claude Code was given Claude Code's flags: %q", got)
	}
}

// What Islet adds to a command, and what it must not touch.
//
// The rule that was missing: a preset decided which flags were added, so a
// command that ran Claude Code under "something else" got none of them, and a
// person who had written their own --resume or --mcp-config got a second copy
// appended after it — in a line they never see, because the stored command is
// what they typed.
func TestResolveAgentCommandAddsWhatIsMissingAndNothingElse(t *testing.T) {
	const mcp = "/var/lib/islet/workspaces/w1/mcp.json"
	const claude = "/root/.local/bin/claude"

	cases := []struct {
		name  string
		cmd   string
		agent *Agent
		want  string
	}{
		{
			"a bare claude gets the path, its conversation and the tools",
			"claude",
			&Agent{Resume: true, SessionUUID: "u1"},
			claude + " --session-id u1 --mcp-config " + mcp,
		},
		{
			"claude with arguments of somebody's own keeps them",
			"claude --model opus-5 --append-system-prompt 'be brief'",
			&Agent{Resume: true, SessionUUID: "u1"},
			claude + " --model opus-5 --append-system-prompt 'be brief' --session-id u1 --mcp-config " + mcp,
		},
		{
			"a second run resumes rather than naming a new conversation",
			"claude",
			&Agent{Resume: true, SessionUUID: "u1", LastStarted: "2026-09-15T10:00:00Z"},
			claude + " --resume u1 --mcp-config " + mcp,
		},
		{
			"a hand-written --resume is left alone",
			"claude --resume abc",
			&Agent{Resume: true, SessionUUID: "u1", LastStarted: "2026-09-15T10:00:00Z"},
			claude + " --resume abc --mcp-config " + mcp,
		},
		{
			"a hand-written --mcp-config is left alone",
			"claude --mcp-config /tmp/mine.json",
			&Agent{},
			claude + " --mcp-config /tmp/mine.json",
		},
		{
			"an absolute path the person chose is not replaced",
			"/usr/local/bin/claude",
			&Agent{},
			"/usr/local/bin/claude --mcp-config " + mcp,
		},
		{
			"asking not to be asked adds the flag once",
			"claude --dangerously-skip-permissions",
			&Agent{SkipPermissions: true},
			claude + " --dangerously-skip-permissions --mcp-config " + mcp,
		},
		{
			"anything that is not Claude Code is run exactly as written",
			"npm run dev -- --host",
			&Agent{Resume: true, SessionUUID: "u1", SkipPermissions: true},
			"npm run dev -- --host",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveAgentCommand(c.cmd, claude, mcp, c.agent); got != c.want {
				t.Errorf("\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// A command may be empty, and that means a shell. It used to mean "the preset
// erased what you typed", which is a different thing and was not asked for.
func TestAnEmptyCommandIsAShell(t *testing.T) {
	for _, preset := range []string{"shell", "custom"} {
		a := &Agent{Name: "w", Preset: preset}
		if err := a.Validate(); err != nil {
			t.Errorf("%s with no command was refused: %v", preset, err)
		}
		if a.Command != "" {
			t.Errorf("%s was given a command it did not ask for: %q", preset, a.Command)
		}
	}
	// And a command under any preset survives.
	a := &Agent{Name: "w", Preset: "shell", Command: "watch -n5 docker ps"}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if a.Command != "watch -n5 docker ps" {
		t.Errorf("the command was erased by its preset: %q", a.Command)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// A session continued from the Claude app brings its own identity.
//
// --teleport picks up a conversation started in the app and the branch it was
// working on. Adding --session-id or --resume beside it asks one process for
// two different conversations, and Claude Code refuses — so an agent set to
// resume must leave a teleport command alone.
func TestATeleportedSessionIsNotGivenASecondIdentity(t *testing.T) {
	a := &Agent{Resume: true, SessionUUID: "11111111-2222-3333-4444-555555555555", LastStarted: "2026-09-21T00:00:00Z"}
	got := resolveAgentCommand("claude --teleport abc123", "/usr/local/bin/claude", "", a)
	if strings.Contains(got, "--resume") || strings.Contains(got, "--session-id") {
		t.Errorf("a second conversation was added to a teleported one: %s", got)
	}
	if !strings.Contains(got, "--teleport abc123") {
		t.Errorf("the teleport was lost: %s", got)
	}
	// An ordinary agent still gets its own conversation back.
	plain := resolveAgentCommand("claude", "/usr/local/bin/claude", "", a)
	if !strings.Contains(plain, "--resume "+a.SessionUUID) {
		t.Errorf("an ordinary agent lost its conversation: %s", plain)
	}
}
