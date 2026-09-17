package api

import (
	"testing"

	"github.com/isletdev/islet/internal/ai"
)

// A workspace agent is a program in a tmux window, so not every model can be
// one. Saying so at the form is the difference between a clear refusal and an
// agent that starts, fails, and leaves somebody reading a shell prompt.
func TestAgentCommand(t *testing.T) {
	cases := []struct {
		p    ai.Provider
		want string
	}{
		{ai.Provider{Kind: ai.KindSubscription}, "claude"},
		{ai.Provider{Kind: ai.KindSubscription, Command: "/root/.local/bin/claude"}, "/root/.local/bin/claude"},
		{ai.Provider{Kind: ai.KindAnthropic}, "claude"},
		// An OpenAI-compatible endpoint answers HTTP and ships no CLI here.
		{ai.Provider{Kind: ai.KindOpenAI}, ""},
	}
	for _, c := range cases {
		if got := AgentCommand(&c.p); got != c.want {
			t.Errorf("%s -> %q, want %q", c.p.Kind, got, c.want)
		}
	}
}
