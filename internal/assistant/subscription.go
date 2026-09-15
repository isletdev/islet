package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Subscription answers through the Claude Code binary in print mode, so the
// person's own Claude subscription pays for it rather than an API key.
//
// It is a provider like the others from the outside and unlike them inside.
// Claude Code runs its own tool loop: it is handed an MCP configuration
// pointing back at this daemon, so it discovers Islet's tools itself, calls
// them over HTTP with the token in that configuration, and returns when it has
// an answer. The `tools` argument here is therefore ignored, and the reply
// never carries tool calls — which means the outer Run loop sees one turn and
// stops, exactly as it should.
//
// The consequence worth knowing: the scopes that apply are the ones on the
// token in the MCP configuration, not the scopes of whoever asked. A workspace
// token is issued for the workspace, and that is what bounds this.
type Subscription struct {
	Bin       string // path to the claude binary
	MCPConfig string // an mcp.json pointing at this daemon
	Model     string
	Dir       string // where to run; the workspace directory
	Timeout   time.Duration
}

func (s *Subscription) Name() string { return "Claude subscription" }

func (s *Subscription) Complete(ctx context.Context, system string, msgs []Message, _ []Tool) (Message, error) {
	bin := s.Bin
	if bin == "" {
		bin = "claude"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return Message{}, fmt.Errorf("claude is not installed on this server: %w", err)
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"--print"}
	if s.MCPConfig != "" {
		args = append(args, "--mcp-config", s.MCPConfig)
		// Print mode cannot ask. Claude Code prompts before using an MCP tool,
		// and with no terminal to prompt on it refuses every one of them — so
		// without this the assistant answers "I could not, permission was
		// never granted" to everything, which reads like a broken tool rather
		// than an ungranted one.
		//
		// The grant is per MCP server and nothing else: no Bash, no file
		// editing, no built-in tools. Those would go around the scopes
		// entirely, and the token in this configuration is the only thing
		// bounding what the assistant can do. Islet's tools are already
		// checked against it on every call.
		if names := mcpServerNames(s.MCPConfig); len(names) > 0 {
			args = append(args, "--allowed-tools", strings.Join(names, " "))
		}
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	cmd := exec.CommandContext(cctx, bin, args...)
	if s.Dir != "" {
		cmd.Dir = s.Dir
	}
	// The conversation goes in on stdin rather than as an argument: a prompt
	// can be long, and an argument list has a limit that a transcript reaches.
	cmd.Stdin = strings.NewReader(transcript(system, msgs))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		// The most common cause by far, and the one whose fix is not obvious.
		if strings.Contains(strings.ToLower(msg), "login") || strings.Contains(msg, "authenticate") {
			return Message{}, fmt.Errorf("claude is not signed in on this server: open a workspace and run claude to sign in (%s)", msg)
		}
		return Message{}, fmt.Errorf("claude: %s", msg)
	}
	return Message{Role: RoleAssistant, Text: strings.TrimSpace(out.String())}, nil
}

// mcpServerNames reads the server names out of an MCP configuration and
// returns them as tool patterns, so the grant follows the file rather than
// assuming the server is called "islet". A configuration that cannot be read
// yields nothing, and the caller then grants nothing, which fails closed.
func mcpServerNames(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil
	}
	out := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		out = append(out, "mcp__"+name)
	}
	sort.Strings(out)
	return out
}

// transcript renders the conversation as text, because print mode takes a
// prompt rather than a message list. Tool results are included: they are what
// an earlier turn found out, and dropping them would make the model repeat work
// it has already done.
func transcript(system string, msgs []Message) string {
	var b strings.Builder
	if strings.TrimSpace(system) != "" {
		b.WriteString(system)
		b.WriteString("\n\n")
	}
	for _, m := range msgs {
		switch {
		case len(m.Results) > 0:
			for _, r := range m.Results {
				b.WriteString("[tool result] ")
				b.WriteString(r.Content)
				b.WriteString("\n")
			}
		case strings.TrimSpace(m.Text) != "":
			if m.Role == RoleAssistant {
				b.WriteString("Assistant: ")
			} else {
				b.WriteString("User: ")
			}
			b.WriteString(m.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}
