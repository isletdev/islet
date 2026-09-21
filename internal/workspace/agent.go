package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Agent is one program running inside a workspace: a tmux window, a command,
// and — for Claude Code — a conversation it keeps coming back to.
//
// The unit exists so a workspace can hold more than one. A worker and a tester
// against the same checkout is the ordinary way to use an agent on a server,
// and with one command per workspace it could only be done by making two
// workspaces that happened to point at the same directory, which then disagreed
// about what was running where.
type Agent struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Preset      string `json:"preset"` // claude | shell | custom
	// ProviderID records which configured model filled this agent's command
	// in. The command is still what runs — it always was — and this is what
	// lets the panel say which model an agent is using and offer the same
	// choice again. Empty is every agent that predates the idea.
	ProviderID string `json:"providerId,omitempty"`
	// Env is passed to tmux when the window is made, which is how an API key
	// reaches the agent without being typed into a shell where it would sit in
	// the scrollback and the history.
	Env     map[string]string `json:"-"`
	Command string            `json:"command"`
	// Resume comes back into the same conversation after a stop, a crash or a
	// reboot. Off means every start is a blank one.
	Resume          bool   `json:"resume"`
	SkipPermissions bool   `json:"skipPermissions"`
	SessionUUID     string `json:"sessionUuid,omitempty"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	LastStarted     string `json:"lastStartedAt"`

	// Derived from tmux, never stored.
	Present bool   `json:"present"` // the window is there
	Running bool   `json:"running"` // and the command, not a shell, is in it
	Doing   string `json:"doing,omitempty"`
}

// shells are what a pane reports when the agent is not running and the window
// is sitting at a prompt.
var shells = map[string]bool{"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true, "ash": true}

// ShellWindow is the window every workspace has besides its agents.
//
// A workspace with agents in it still needs somewhere to run `git status`, and
// borrowing an agent's window means interrupting the agent to do it.
const ShellWindow = "shell"

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Validate normalises an agent and explains anything it refuses.
func (a *Agent) Validate() error {
	a.Name = strings.ToLower(strings.TrimSpace(a.Name))
	a.Command = strings.TrimSpace(a.Command)
	if !nameRe.MatchString(a.Name) {
		return errors.New("the name must be lowercase letters, digits and dashes, and start with a letter or digit")
	}
	if a.Name == ShellWindow {
		return errors.New(`"shell" is the name of the workspace's own shell window; pick another`)
	}
	switch a.Preset {
	// A preset is where the form starts, not what the agent is allowed to be.
	// It used to be the second thing: choosing "a shell" erased whatever
	// command had been typed, and choosing "something else" turned off resume
	// and the permission flag whether or not the command was Claude Code — so
	// `claude --model opus-5` could not resume, and there was no way to run a
	// shell with anything in it. What actually decides is the command.
	case "claude":
		if a.Command == "" {
			a.Command = "claude"
		}
	case "shell", "custom":
		// An empty command is a shell. That is a choice, not a mistake.
	default:
		return errors.New("preset must be claude, shell or custom")
	}
	if strings.ContainsAny(a.Command, "\n\r") {
		return errors.New("the command must be a single line")
	}
	// A conversation id belongs to anything that runs Claude Code, whichever
	// preset was picked to get there.
	if a.SessionUUID == "" && isClaude(a.Command) {
		a.SessionUUID = newUUID()
	}
	return nil
}

// ---- tmux windows ---------------------------------------------------------

// target names an agent's window to tmux.
func target(wsID, name string) string { return SessionName(wsID) + ":" + name }

// AttachAgentArgv joins the workspace and selects this agent's window.
//
// select-window before attach-session, as two commands in one invocation, so
// the window is chosen unambiguously; passing session:window where tmux expects
// a target-session works by leniency rather than by contract.
func (s *Service) AttachAgentArgv(wsID, name string) []string {
	return []string{"tmux", "-S", s.sock,
		"select-window", "-t", target(wsID, name), ";",
		"attach-session", "-d", "-t", SessionName(wsID)}
}

// windowState reads what each window of a session is doing, in one call.
func (s *Service) windowState(ctx context.Context, wsID string) map[string]string {
	// A space separates the two fields, not a tab. A window name is validated to
	// letters, digits and dashes and a pane command is one word, so there is
	// nothing here that needs protecting from a split - and one less character to
	// lose between Go, the exec call and tmux's own format parser.
	out, err := s.tmuxRead(ctx, "list-windows", "-t", SessionName(wsID),
		"-F", "#{window_name} #{pane_current_command}")
	if err != nil {
		// Not an error worth surfacing: a session that is not running has no
		// windows, which is the ordinary case. It is worth being able to see.
		s.log.Debug("tmux could not list the windows", "workspace", wsID, "err", err)
		return nil
	}
	state := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		switch len(f) {
		case 0:
		case 1:
			state[f[0]] = ""
		default:
			state[f[0]] = f[1]
		}
	}
	return state
}

// ensureWindow creates the agent's window if the session does not have it.
func (s *Service) ensureWindow(ctx context.Context, actor string, w *Workspace, a *Agent) error {
	if err := s.ensure(ctx, actor, w); err != nil {
		return err
	}
	if st := s.windowState(ctx, w.ID); st != nil {
		if _, ok := st[a.Name]; ok {
			return nil
		}
	}
	args := []string{"new-window", "-d", "-t", SessionName(w.ID), "-n", a.Name, "-c", w.Directory}
	// -e rather than an export typed into the pane: a key sent with send-keys
	// lands in the scrollback, in the shell's history, and in anything that
	// later attaches to the window.
	for k, v := range a.Env {
		if k != "" && v != "" {
			args = append(args, "-e", k+"="+v)
		}
	}
	_, err := s.tmux(ctx, actor, args...)
	return err
}

// EnsureAgentWindow makes sure there is a window to attach to.
//
// Opening an agent that has never been started should show an empty prompt in
// the right directory, not an error about a window that does not exist.
func (s *Service) EnsureAgentWindow(ctx context.Context, actor, wsID, id string) error {
	w, err := s.Get(ctx, wsID)
	if err != nil {
		return err
	}
	a, err := s.GetAgent(ctx, wsID, id)
	if err != nil {
		return err
	}
	return s.ensureWindow(ctx, actor, w, a)
}

// launchAgent is the command line an agent actually runs.
//
// The stored command is what the person typed and is left alone. This is the
// resolved one: an absolute path when the binary is not on PATH, the MCP
// configuration when the workspace has one, and — the point of the exercise —
// the flags that put the agent back into its own conversation rather than a new
// one or, worse, a sibling's.
func (s *Service) launchAgent(ctx context.Context, w *Workspace, a *Agent) string {
	cmd := strings.TrimSpace(a.Command)
	if cmd == "" {
		return ""
	}
	claudePath := ""
	if isClaude(cmd) {
		claudePath = s.ClaudePath(ctx)
	}
	mcp := ""
	if w.MCPEnabled {
		if p := s.mcpPath(w.ID); fileExists(p) {
			mcp = p
		}
	}
	return resolveAgentCommand(cmd, claudePath, mcp, a)
}

// isClaude reports whether a command line runs Claude Code, which is what
// decides whether Claude Code's flags mean anything.
func isClaude(cmd string) bool {
	head, _, _ := strings.Cut(strings.TrimSpace(cmd), " ")
	// path, not filepath: this command line is executed by a shell on the
	// Linux server, so "/usr/bin/claude" is absolute whatever the machine this
	// daemon was compiled on thinks.
	return head != "" && path.Base(head) == "claude"
}

// resolveAgentCommand adds what Islet knows to what the person wrote.
//
// Two rules, and the second is the one that was missing. Flags are added
// because the command is Claude Code, not because a preset said so. And
// anything already on the line is left alone: somebody who has written their
// own --model, --mcp-config or --resume meant it, and a second copy appended
// after it is at best ignored and at worst an error they cannot see, since the
// line that runs is not the line they typed.
func resolveAgentCommand(cmd, claudePath, mcpConfig string, a *Agent) string {
	if !isClaude(cmd) {
		return cmd
	}
	head, rest, _ := strings.Cut(cmd, " ")
	if claudePath != "" && !strings.HasPrefix(head, "/") && claudePath != head {
		cmd = claudePath
		if rest != "" {
			cmd += " " + rest
		}
	}
	has := func(flag string) bool { return strings.Contains(cmd, flag) }
	// --teleport carries its own identity: it continues a session started in
	// the Claude app, on the branch that session was working on. Adding
	// --session-id or --resume beside it asks for two different conversations
	// in one process, and Claude Code refuses.
	if a.Resume && a.SessionUUID != "" && !has("--resume") && !has("--session-id") && !has("--continue") && !has("--teleport") {
		// --session-id names the conversation on the first run; --resume
		// reopens that exact one afterwards. --continue is deliberately not
		// used: it means "the most recent conversation in this directory", and
		// two agents in one workspace share a directory, so it would hand both
		// of them the same history and lose one of them.
		if a.LastStarted == "" {
			cmd += " --session-id " + a.SessionUUID
		} else {
			cmd += " --resume " + a.SessionUUID
		}
	}
	if mcpConfig != "" && !has("--mcp-config") {
		cmd += " --mcp-config " + mcpConfig
	}
	if a.SkipPermissions && !has("--dangerously-skip-permissions") {
		cmd += " --dangerously-skip-permissions"
	}
	return cmd
}

// StartAgent puts the agent's window up and types its command into it.
func (s *Service) StartAgent(ctx context.Context, actor, wsID, id string) error {
	w, err := s.Get(ctx, wsID)
	if err != nil {
		return err
	}
	a, err := s.GetAgent(ctx, wsID, id)
	if err != nil {
		return err
	}
	if s.AgentEnv != nil {
		a.Env = s.AgentEnv(ctx, a.ProviderID)
	}
	if a.Preset == "claude" && s.ClaudePath(ctx) == "" {
		return errors.New("Claude Code is not installed on this server. Install it from the Workspaces page, or run the installer yourself: curl -fsSL https://claude.ai/install.sh | bash")
	}
	if err := s.ensureWindow(ctx, actor, w, a); err != nil {
		return err
	}
	cmd := s.launchAgent(ctx, w, a)
	if cmd == "" {
		return nil // a shell agent is its window; there is nothing to type
	}
	if _, err := s.tmux(ctx, actor, "send-keys", "-t", target(wsID, a.Name), cmd, "Enter"); err != nil {
		return err
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`UPDATE workspace_agents SET last_started_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		 WHERE id = ? AND server_id = ?`, a.ID, s.st.ServerID); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "workspace.agent.start", w.Name+"/"+a.Name, a.Command)
	return nil
}

// StopAgent closes the agent's window, and with it whatever was running.
func (s *Service) StopAgent(ctx context.Context, actor, wsID, id string) error {
	w, err := s.Get(ctx, wsID)
	if err != nil {
		return err
	}
	a, err := s.GetAgent(ctx, wsID, id)
	if err != nil {
		return err
	}
	if _, err := s.tmux(ctx, actor, "kill-window", "-t", target(wsID, a.Name)); err != nil {
		return nil // already gone is the state that was asked for
	}
	_ = s.st.Audit(ctx, actor, "workspace.agent.stop", w.Name+"/"+a.Name, "")
	return nil
}

// AgentHistory is what this agent's window has on screen and above it.
func (s *Service) AgentHistory(ctx context.Context, actor, wsID, id string, lines int) (string, error) {
	a, err := s.GetAgent(ctx, wsID, id)
	if err != nil {
		return "", err
	}
	if lines <= 0 || lines > 20000 {
		lines = 5000
	}
	return s.tmuxRead(ctx, "capture-pane", "-p", "-S", "-"+fmt.Sprint(lines), "-t", target(wsID, a.Name))
}

// ---- storage --------------------------------------------------------------

const agentCols = `id, workspace_id, name, preset, provider_id, command, resume, session_uuid, skip_permissions, created_at, updated_at, last_started_at`

func scanAgent(sc interface{ Scan(...any) error }) (Agent, error) {
	var a Agent
	var res, skip int
	err := sc.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Preset, &a.ProviderID, &a.Command, &res, &a.SessionUUID, &skip,
		&a.CreatedAt, &a.UpdatedAt, &a.LastStarted)
	a.Resume, a.SkipPermissions = res == 1, skip == 1
	return a, err
}

// Agents lists a workspace's agents, with live state from tmux.
func (s *Service) Agents(ctx context.Context, wsID string) ([]Agent, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT `+agentCols+` FROM workspace_agents WHERE workspace_id = ? AND server_id = ? ORDER BY created_at`,
		wsID, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	state := s.windowState(ctx, wsID)
	for i := range out {
		cmd, ok := state[out[i].Name]
		out[i].Present = ok
		out[i].Doing = cmd
		// A window stays open at a prompt after its command exits, so its
		// existence is not the same question as whether the agent is going.
		out[i].Running = ok && cmd != "" && !shells[cmd]
	}
	return out, nil
}

// GetAgent reads one agent of one workspace.
func (s *Service) GetAgent(ctx context.Context, wsID, id string) (*Agent, error) {
	row := s.st.DB.QueryRowContext(ctx,
		`SELECT `+agentCols+` FROM workspace_agents WHERE id = ? AND workspace_id = ? AND server_id = ?`,
		id, wsID, s.st.ServerID)
	a, err := scanAgent(row)
	if err != nil {
		return nil, ErrNotFound
	}
	return &a, nil
}

// SaveAgent inserts or updates one.
func (s *Service) SaveAgent(ctx context.Context, actor, wsID string, a *Agent) (*Agent, error) {
	w, err := s.Get(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	res, skip := 0, 0
	if a.Resume {
		res = 1
	}
	if a.SkipPermissions {
		skip = 1
	}
	if a.ID == "" {
		a.ID = newID()
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO workspace_agents
			(id, server_id, workspace_id, name, preset, provider_id, command, resume, session_uuid, skip_permissions)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, s.st.ServerID, wsID, a.Name, a.Preset, a.ProviderID, a.Command, res, a.SessionUUID, skip)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("this workspace already has an agent with that name")
			}
			return nil, err
		}
	} else {
		old, err := s.GetAgent(ctx, wsID, a.ID)
		if err != nil {
			return nil, err
		}
		// Renaming moves the window, so the name on screen and the name in the
		// panel do not drift apart.
		if old.Name != a.Name {
			_, _ = s.tmux(ctx, actor, "rename-window", "-t", target(wsID, old.Name), a.Name)
		}
		r, err := s.st.DB.ExecContext(ctx, `UPDATE workspace_agents
			SET name=?, preset=?, provider_id=?, command=?, resume=?, session_uuid=?, skip_permissions=?,
			    updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id = ? AND workspace_id = ? AND server_id = ?`,
			a.Name, a.Preset, a.ProviderID, a.Command, res, a.SessionUUID, skip, a.ID, wsID, s.st.ServerID)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("this workspace already has an agent with that name")
			}
			return nil, err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return nil, ErrNotFound
		}
	}
	_ = s.st.Audit(ctx, actor, "workspace.agent.save", w.Name+"/"+a.Name, a.Command)
	return s.GetAgent(ctx, wsID, a.ID)
}

// DeleteAgent removes the agent and closes its window.
func (s *Service) DeleteAgent(ctx context.Context, actor, wsID, id string) error {
	a, err := s.GetAgent(ctx, wsID, id)
	if err != nil {
		return err
	}
	_, _ = s.tmux(ctx, actor, "kill-window", "-t", target(wsID, a.Name))
	if _, err := s.st.DB.ExecContext(ctx,
		`DELETE FROM workspace_agents WHERE id = ? AND workspace_id = ? AND server_id = ?`,
		id, wsID, s.st.ServerID); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "workspace.agent.delete", a.Name, "")
	return nil
}
