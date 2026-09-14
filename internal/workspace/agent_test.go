package workspace

import "testing"

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

	// A shell agent has nothing to resume and nothing to skip permissions for.
	sh := &Agent{Name: "runner", Preset: "shell", Resume: true, SkipPermissions: true}
	if err := sh.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if sh.Resume || sh.SkipPermissions {
		t.Error("a shell agent kept flags that only mean something to Claude Code")
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
