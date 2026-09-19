package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// Ready: the binary it drives. Whether it has been signed in to is a separate
// question, asked and answered where somebody can act on it — Settings → AI
// installs it and says whether it has a login.
func (s *Subscription) Ready() error {
	if strings.TrimSpace(s.Bin) == "" {
		return errors.New("Claude Code is not installed on this server")
	}
	return nil
}

func (s *Subscription) Complete(ctx context.Context, system string, msgs []Message, tools []Tool) (Message, error) {
	return s.CompleteStream(ctx, system, msgs, tools, nil)
}

// CompleteStream runs the binary and reports each tool as it is used.
//
// Claude Code's own loop is the reason this is not like the other providers.
// It calls Islet's tools itself, so a run that made nine calls would otherwise
// reach the panel as a single turn several minutes later with nothing in
// between — and on a phone, several minutes of nothing is indistinguishable
// from a hang. `--output-format stream-json` writes one JSON object per event
// as it happens, which is exactly the progress worth showing.
func (s *Subscription) CompleteStream(ctx context.Context, system string, msgs []Message, _ []Tool, obs *Observer) (Message, error) {
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

	// --verbose is not optional here: stream-json refuses without it.
	//
	// --include-partial-messages is what makes this feel like a conversation
	// rather than a form submission. Without it Claude Code emits one event per
	// completed message, so a paragraph arrives whole after however long it
	// took to write — measured here at 7.7 seconds of nothing and then all of
	// it. With it, the first words are on screen in three, and the rest follow
	// as they are written.
	args := []string{"--print", "--output-format", "stream-json", "--verbose", "--include-partial-messages"}
	// Nothing may answer a permission prompt, so anything that would ask is
	// refused rather than waiting for a terminal that does not exist.
	args = append(args, "--permission-prompts", "none", "--settings", claudeSettings())
	if s.MCPConfig != "" {
		// Only the servers in that file. Without this, an MCP server configured
		// for the person running the daemon would be loaded too, and the
		// assistant would quietly have tools nobody granted it.
		args = append(args, "--mcp-config", s.MCPConfig, "--strict-mcp-config")
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
	// Not CommandContext: cancellation here has to kill the process group and
	// then unblock a read that a surviving child can otherwise hold open
	// forever, which is more than a context kill does. See below.
	cmd := exec.Command(bin, args...)
	setProcAttrs(cmd)
	if s.Dir != "" {
		cmd.Dir = s.Dir
	}
	// The conversation goes in on stdin rather than as an argument: a prompt
	// can be long, and an argument list has a limit that a transcript reaches.
	cmd.Stdin = strings.NewReader(transcript(system, msgs))
	// A pipe this code owns both ends of, rather than StdoutPipe, so the read
	// can be unblocked from here. That is the whole point: when the process is
	// gone, anything still holding the write end is a child that outlived it,
	// and waiting on it is waiting forever.
	pr, pw, err := os.Pipe()
	if err != nil {
		return Message{}, err
	}
	cmd.Stdout = pw
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return Message{}, fmt.Errorf("claude: %w", err)
	}
	// The parent's copy of the write end, dropped now so the only holders are
	// the process and its children.
	pw.Close()

	type readResult struct {
		answer string
		done   []ToolRun
		err    error
	}
	read := make(chan readResult, 1)
	go func() {
		a, d, e := readClaudeStream(pr, obs)
		read <- readResult{a, d, e}
	}()

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	// Stopping is killing the group. Without this a stopped run leaves the
	// binary and its MCP server running, and the next question waits behind
	// them.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-cctx.Done():
			killTree(cmd)
		case <-stopped:
		}
	}()

	werr := <-waited
	// The process has gone. Give the reader a moment to drain what is still in
	// the pipe, then close it — after which a child holding the other end is
	// somebody else's problem rather than this run's.
	drained := time.AfterFunc(drainGrace, func() { pr.Close() })
	res := <-read
	drained.Stop()
	pr.Close()
	answer, done, perr := res.answer, res.done, res.err
	// A process that exited cleanly said what it had to say; an error from the
	// reader after that is the pipe being closed on a child, not a failure of
	// the run.
	if errors.Is(perr, errStreamCut) && werr == nil && ctx.Err() == nil {
		perr = nil
	}
	if perr != nil && werr == nil {
		werr = perr
	}
	// Whatever was said before it ended, whichever way it ended. Returned with
	// the error rather than instead of it: a turn that was interrupted halfway
	// is still most of an answer, and throwing it away is what made pressing
	// Stop feel like undo.
	sofar := Message{Role: RoleAssistant, Text: strings.TrimSpace(answer), Tools: done}
	if werr != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = werr.Error()
		}
		// Being stopped is not a failure to report as one. The process is
		// killed by the context going away, and what it had written is the
		// answer somebody chose to stop reading.
		if ctx.Err() != nil {
			return sofar, ctx.Err()
		}
		// The most common cause by far, and the one whose fix is not obvious.
		if strings.Contains(strings.ToLower(msg), "login") || strings.Contains(msg, "authenticate") {
			return sofar, fmt.Errorf("claude is not signed in on this server: open a workspace and run claude to sign in (%s)", msg)
		}
		return sofar, fmt.Errorf("claude: %s", msg)
	}
	if sofar.Text == "" {
		return sofar, errors.New("claude answered nothing; check that it is signed in on this server")
	}
	return sofar, nil
}

// claudeEvent is the part of one stream-json line this cares about. The format
// carries a great deal more — token counts, costs, session ids — and reading
// only these four shapes means a new field upstream cannot break the parse.
type claudeEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			ID      string          `json:"id"`
			Name    string          `json:"name"`
			Input   map[string]any  `json:"input"`
			ToolUse string          `json:"tool_use_id"`
			Content json.RawMessage `json:"content"`
			IsError bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
	// Event carries the partial-message stream: one of these per few tokens,
	// which is what live text is made of.
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
}

// readClaudeStream turns the binary's event stream into observer calls and
// returns the final answer.
func readClaudeStream(r io.Reader, obs *Observer) (string, []ToolRun, error) {
	sc := bufio.NewScanner(r)
	// A tool result can be large — a container listing, a file — and the
	// default 64 KB limit would end the scan mid-run with an answer already
	// half-reported.
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	calls := map[string]ToolCall{}
	started := map[string]time.Time{}
	var done []ToolRun
	var answer string
	var failure string
	// What has been said so far, from two angles. `said` is the completed
	// assistant messages; `partial` is the live deltas, which is all there is
	// when a run is stopped or the process dies mid-sentence. One of them is
	// what somebody was reading when it ended, and losing it is the thing this
	// whole arrangement exists to prevent.
	var said, partial strings.Builder
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e claudeEvent
		if err := json.Unmarshal(line, &e); err != nil {
			continue // a line this does not understand is not a reason to stop
		}
		switch e.Type {
		case "stream_event":
			// Live text, a few tokens at a time. Reported and not accumulated:
			// the completed assistant event below carries the same words and is
			// the authority for what gets stored, so counting both would say
			// everything twice.
			if e.Event.Type == "content_block_delta" && e.Event.Delta.Text != "" {
				partial.WriteString(e.Event.Delta.Text)
				obs.text(e.Event.Delta.Text)
			}
		case "assistant":
			for _, b := range e.Message.Content {
				switch b.Type {
				case "text":
					// Reported only when nothing streamed it already. A build
					// of Claude Code that does not know --include-partial-
					// messages sends no deltas at all, and on that one this is
					// the only chance to show the text; where deltas did
					// arrive, saying it again would say everything twice.
					if partial.Len() == 0 {
						obs.text(b.Text)
					}
					said.WriteString(b.Text)
				case "tool_use":
					if internalTool(b.Name) {
						// Looking up which tool to use is the model talking to
						// itself, not something happening to the server, and on
						// a phone it filled the screen with `query=select:…`.
						// Everything else is reported, including anything the
						// deny list failed to stop — a built-in appearing here
						// is a hole worth seeing.
						continue
					}
					c := ToolCall{ID: b.ID, Name: shortToolName(b.Name), Input: b.Input}
					calls[b.ID] = c
					started[b.ID] = time.Now()
					obs.toolStart(c)
				}
			}
		case "user":
			for _, b := range e.Message.Content {
				if b.Type != "tool_result" {
					continue
				}
				c, ok := calls[b.ToolUse]
				if !ok {
					// Its start was not reported, so neither is its end.
					continue
				}
				out := resultText(b.Content)
				took := time.Since(started[b.ToolUse])
				obs.toolEnd(c, ToolResult{CallID: b.ToolUse, Content: out, IsError: b.IsError}, took)
				// Kept with the turn so the conversation still says what was
				// done when it is read back tomorrow, on another device. The
				// output is trimmed: a container listing is not a thing to
				// store in full on every turn.
				const keep = 2 << 10
				if len(out) > keep {
					out = out[:keep] + "\n… truncated"
				}
				done = append(done, ToolRun{Name: c.Name, Input: c.Input, MS: took.Milliseconds(), OK: !b.IsError, Output: out})
				delete(calls, b.ToolUse)
				delete(started, b.ToolUse)
			}
		case "result":
			answer = e.Result
			if e.IsError {
				failure = e.Result
				if strings.TrimSpace(failure) == "" {
					failure = e.Subtype
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		// Marked as what it is: the stream stopped, which is not the same as
		// the run reporting a failure. One of them is worth showing somebody
		// and the other is a pipe being closed on a process that has already
		// exited normally.
		return sofar(answer, said, partial), done, fmt.Errorf("%w: %v", errStreamCut, err)
	}
	if failure != "" {
		return sofar(answer, said, partial), done, errors.New(failure)
	}
	if strings.TrimSpace(answer) == "" {
		// A result event with nothing in it, which happens when the run was cut
		// short. Whatever was already said is the answer.
		answer = sofar(answer, said, partial)
	}
	return answer, done, nil
}

// sofar is the best account of what was said, in the order of how complete each
// version is: the final result, then the messages that completed, then the
// half-written sentence that was on screen when it stopped.
func sofar(answer string, said, partial strings.Builder) string {
	if strings.TrimSpace(answer) != "" {
		return answer
	}
	if strings.TrimSpace(said.String()) != "" {
		return said.String()
	}
	return partial.String()
}

// internalTool is a Claude Code tool that does nothing to the server.
//
// Only tool discovery qualifies: it is the model reading the tool list, which
// is its own business. Everything that touches anything is reported.
func internalTool(name string) bool { return name == "ToolSearch" }

// shortToolName drops the MCP prefix, so the panel shows list_domains rather
// than mcp__islet__list_domains. A built-in tool has no prefix and is left as
// it is.
func shortToolName(name string) string {
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		if _, tool, ok := strings.Cut(rest, "__"); ok && tool != "" {
			return tool
		}
	}
	return name
}

// resultText pulls readable text out of a tool result, which the format gives
// either as a string or as a list of content blocks.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var out []string
		for _, b := range blocks {
			if b.Text != "" {
				out = append(out, b.Text)
			}
		}
		if len(out) > 0 {
			return strings.Join(out, "\n")
		}
	}
	return string(raw)
}

// deniedTools are Claude Code's own tools, which this assistant must not have.
//
// The reason is the whole design. Islet's authority is the token in the MCP
// configuration and the scopes on it; every tool call through that server is
// checked against them and written to the audit log. A built-in tool goes round
// all of it — the daemon runs as root, so `Bash` is a root shell and `Read` is
// every file on the machine, neither of them scoped, neither of them recorded.
//
// Granting the MCP server with --allowed-tools does not restrict anything: it
// says which tools need no approval, and measurement is how that was found out.
// Asked to run `id -u` with only that flag, the assistant ran it and answered
// 0. Nor is "nobody can approve" enough on its own: Claude Code treats some
// commands as safe and runs them without asking, so `Read` was refused in that
// state and `Bash` was not.
//
// This list is a floor rather than a ceiling, and it is honest to say so: a
// tool added upstream and classified as safe would not be on it. Two things
// stand behind it — permission prompts answered by nobody, which catches
// anything that asks, and the fact that the useful paths are all through the
// MCP server anyway, where the scopes are. The durable fix is to run this
// process as somebody other than root, which is a separate change because the
// subscription's credentials live in root's home.
var deniedTools = []string{
	"Bash", "BashOutput", "KillShell",
	"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep",
	"WebFetch", "WebSearch",
	"Task", "Monitor", "Skill", "Workflow", "Artifact",
	"PushNotification", "SendMessage", "SendUserFile", "RemoteTrigger", "ScheduleWakeup",
	"CronCreate", "CronDelete", "CronList",
}

// claudeSettings is the permission policy, as the JSON --settings takes.
func claudeSettings() string {
	b, err := json.Marshal(map[string]any{
		"permissions": map[string]any{
			"deny": deniedTools,
			// Everything else asks, and nothing can answer.
			"defaultMode": "manual",
		},
	})
	if err != nil {
		// Unreachable with a literal map, and a broken policy must not become
		// no policy: an empty deny list is the permissive case.
		return `{"permissions":{"deny":["Bash","Read","Write","Edit","Task"],"defaultMode":"manual"}}`
	}
	return string(b)
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
		case strings.TrimSpace(m.Prompt()) != "":
			if m.Role == RoleAssistant {
				b.WriteString("Assistant: ")
			} else {
				b.WriteString("User: ")
			}
			b.WriteString(m.Prompt())
			b.WriteString("\n")
		}
	}
	return b.String()
}

// drainGrace is how long the reader gets to finish what is already in the pipe
// after the process has exited. Short: everything worth reading was written
// before the exit, and the only reason to wait at all is that the last event
// and the exit race each other.
const drainGrace = 2 * time.Second

// errStreamCut is the stream ending rather than the run failing: the pipe was
// closed, usually by this code once the process had already exited and a child
// was still holding it open.
var errStreamCut = errors.New("the stream ended")
