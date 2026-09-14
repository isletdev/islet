// Package workspace keeps long-running work alive on the server.
//
// A workspace is a directory, a command, and a tmux session. tmux is the whole
// mechanism: the command it runs is a child of tmux rather than of isletd, so
// it survives a closed browser, a dropped connection, and an `islet update`
// that restarts the daemon underneath it. The same session can be reached with
// `tmux attach` over SSH, which matters — a panel that is the only way back to
// your own work is a panel you cannot afford to have go down.
//
// What this is for is running an agent next to the thing it is changing: make a
// change, look at the live service, come back and carry on, without a laptop
// staying awake in the middle.
package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/internal/store"
)

// ErrNotFound is returned for an id that is not on this server.
var ErrNotFound = errors.New("workspace not found")

// ErrNoTmux is returned when the host has no tmux. Everything here is built on
// it, so this is reported rather than worked around.
var ErrNoTmux = errors.New("tmux is not installed")

// Workspace is one persistent session.
type Workspace struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Directory  string `json:"directory"`
	Preset     string `json:"preset"` // claude | shell | custom
	Command    string `json:"command"`
	MCPEnabled bool   `json:"mcpEnabled"`
	// SkipPermissions runs Claude Code with --dangerously-skip-permissions, so
	// it acts without asking first. Useful when nobody is watching; the panel
	// says what it costs.
	SkipPermissions bool   `json:"skipPermissions"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	LastAttach      string `json:"lastAttachedAt"`

	// Derived from tmux, never stored.
	Running bool   `json:"running"`
	Started string `json:"started,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Service owns the workspaces table and the tmux sessions behind it.
type Service struct {
	st   *store.Store
	run  *cmdrun.Runner
	bus  *notify.Bus
	log  *slog.Logger
	dir  string // <dataDir>/workspaces
	sock string // <dataDir>/tmux.sock — the server every session lives on
}

// New builds the service. dataDir is the daemon's data directory; per-workspace
// files (an MCP config, for now) live under <dataDir>/workspaces/<id>.
func New(st *store.Store, run *cmdrun.Runner, bus *notify.Bus, dataDir string, log *slog.Logger) *Service {
	return &Service{
		st: st, run: run, bus: bus, log: log,
		dir:  filepath.Join(dataDir, "workspaces"),
		sock: filepath.Join(dataDir, "tmux.sock"),
	}
}

// Socket is the tmux server every workspace lives on.
//
// Named explicitly rather than left to tmux's default of /tmp/tmux-<uid>/. The
// systemd unit sets PrivateTmp=yes, so the daemon's /tmp is a namespace of its
// own and a fresh one on every restart: sessions created on the default socket
// are unreachable the moment isletd restarts, and were never reachable from an
// SSH shell at all — which quietly broke the one escape hatch this feature
// promised. With a path under the data directory, `tmux -S <path> attach` works
// from any shell on the box.
func (s *Service) Socket() string { return s.sock }

// Dir is where this workspace's own files live.
func (s *Service) Dir(id string) string { return filepath.Join(s.dir, id) }

// ---- tmux ----------------------------------------------------------------

// SessionName is the tmux session for a workspace.
//
// Derived from the id rather than the name so renaming a workspace does not
// strand a session, and so a name can hold characters tmux would read as a
// target pattern.
func SessionName(id string) string { return "islet-ws-" + id }

// HasTmux reports whether the host can run any of this.
func (s *Service) HasTmux(ctx context.Context) bool {
	_, err := s.run.Run(ctx, "system", "tmux", "-V")
	return err == nil
}

// InstallTmux installs tmux with the system package manager, streaming output.
func (s *Service) InstallTmux(ctx context.Context, actor string) (rc interface {
	Read([]byte) (int, error)
	Close() error
}, wait func() error, err error) {
	return s.run.Stream(ctx, actor, "sh", "-c",
		"apt-get update && apt-get install -y tmux || dnf install -y tmux || apk add --no-cache tmux")
}

// ClaudePath is where Claude Code is on this machine, or "" when it is not.
//
// PATH alone is not enough to answer this. The official installer puts the
// binary in ~/.local/bin, which a non-login shell started by tmux may not have
// on its PATH — so "claude: command not found" is the normal outcome of a
// perfectly good install, and the fix is to use the path we found rather than
// to hope.
func (s *Service) ClaudePath(ctx context.Context) string {
	if s.run == nil {
		return "" // no runner: a unit test, not a server
	}
	if out, err := s.run.Run(ctx, "system", "sh", "-c", "command -v claude 2>/dev/null"); err == nil {
		if p := strings.TrimSpace(out.Stdout); p != "" {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "claude"),
		"/usr/local/bin/claude",
		"/usr/bin/claude",
	} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// InstallClaude runs the official installer, streaming what it says.
//
// The npm package is the fallback rather than the first choice: the native
// installer is what Anthropic ships, and it does not need a Node runtime on a
// server that may not have one.
func (s *Service) InstallClaude(ctx context.Context, actor string) (rc interface {
	Read([]byte) (int, error)
	Close() error
}, wait func() error, err error) {
	return s.run.Stream(ctx, actor, "sh", "-c",
		"curl -fsSL https://claude.ai/install.sh | bash "+
			"|| npm install -g @anthropic-ai/claude-code")
}

// launch is the command line a workspace actually runs.
//
// The stored command is what the person typed and is left alone in the list and
// in the editor; this is the resolved version, with an absolute path when the
// binary is not on PATH and the MCP configuration when there is one.
func (s *Service) launch(ctx context.Context, w *Workspace) string {
	cmd := strings.TrimSpace(w.Command)
	if cmd == "" {
		return ""
	}
	if w.Preset == "claude" {
		head, rest, _ := strings.Cut(cmd, " ")
		if path.Base(head) == "claude" && !strings.HasPrefix(head, "/") {
			if p := s.ClaudePath(ctx); p != "" && p != head {
				cmd = p
				if rest != "" {
					cmd += " " + rest
				}
			}
		}
		if w.MCPEnabled {
			if p := s.mcpPath(w.ID); fileExists(p) {
				cmd += " --mcp-config " + p
			}
		}
		if w.SkipPermissions && !strings.Contains(cmd, "--dangerously-skip-permissions") {
			cmd += " --dangerously-skip-permissions"
		}
	}
	return cmd
}

func (s *Service) tmux(ctx context.Context, actor string, args ...string) (string, error) {
	// The server has to exist before any client command runs, or that command
	// starts one here, inside isletd, which is the thing being avoided. See
	// ensureServer: this is a socket dial when the server is already up.
	s.ensureServer(ctx, actor)
	// -S before the subcommand selects the server; capture-pane's own -S is a
	// different flag and comes after, which is why this one goes in front.
	res, err := s.run.Run(ctx, actor, "tmux", append([]string{"-S", s.sock}, args...)...)
	return strings.TrimSpace(res.Stdout), err
}

// running reports whether the session exists, and when it started.
func (s *Service) running(ctx context.Context, id string) (bool, string) {
	out, err := s.tmux(ctx, "system", "display-message", "-p", "-t", SessionName(id), "#{session_created}")
	if err != nil || out == "" {
		return false, ""
	}
	return true, out
}

// ensure creates the session if it is not there. It never runs the workspace's
// command: starting the agent is a separate, deliberate act, so that a session
// coming back after a reboot does not silently resume work nobody is watching.
func (s *Service) ensure(ctx context.Context, actor string, w *Workspace) error {
	if ok, _ := s.running(ctx, w.ID); ok {
		return nil
	}
	if !s.HasTmux(ctx) {
		return ErrNoTmux
	}
	if err := os.MkdirAll(filepath.Dir(s.sock), 0o700); err != nil {
		return err
	}
	// -n names the first window, so every workspace has a plain shell of
	// its own beside whatever agents it runs. Borrowing an agent's window to
	// check `git status` means interrupting the agent to do it.
	if _, err := s.tmux(ctx, actor, "new-session", "-d", "-s", SessionName(w.ID), "-n", ShellWindow, "-c", w.Directory); err != nil {
		return err
	}
	// tmux's status bar names the session and its windows along the bottom of
	// every pane. Inside the panel that is a second, worse copy of the workspace
	// and agent names already on screen, and it eats a row of the terminal. It
	// stays off for Islet's sessions; attaching over SSH is unaffected, since a
	// person there can turn it back on for their own client.
	_, _ = s.tmux(ctx, actor, "set-option", "-t", SessionName(w.ID), "status", "off")
	return nil
}

// AttachArgv is the command that joins a session from a PTY.
//
// -d detaches every other client first. tmux sizes a session to its smallest
// attached client, so a tab left open on a phone would otherwise squeeze a
// desktop session down to its width. The most recent viewer wins.
func (s *Service) AttachArgv(id string) []string {
	return []string{"tmux", "-S", s.sock, "attach-session", "-d", "-t", SessionName(id)}
}

// Start types the workspace's command into the session.
//
// send-keys rather than passing the command to new-session, so it lands in the
// session's own history exactly as if it had been typed: visible in the
// scrollback, re-runnable with the up arrow, and obvious to anyone who attaches
// later and wonders what is running.
func (s *Service) Start(ctx context.Context, actor, id string) error {
	w, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(w.Command) == "" {
		return errors.New("this workspace has no command to run; open it and type what you want")
	}
	if err := s.ensure(ctx, actor, w); err != nil {
		return err
	}
	if w.Preset == "claude" && s.ClaudePath(ctx) == "" {
		return errors.New("Claude Code is not installed on this server. Install it from the Workspaces page, or run the installer yourself: curl -fsSL https://claude.ai/install.sh | bash")
	}
	cmd := s.launch(ctx, w)
	if _, err := s.tmux(ctx, actor, "send-keys", "-t", SessionName(w.ID), cmd, "Enter"); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "workspace.start", w.Name, w.Command)
	return nil
}

// Stop kills the session and everything in it.
func (s *Service) Stop(ctx context.Context, actor, id string) error {
	w, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if ok, _ := s.running(ctx, id); !ok {
		return nil
	}
	if _, err := s.tmux(ctx, actor, "kill-session", "-t", SessionName(id)); err != nil {
		return err
	}
	_ = s.st.Audit(ctx, actor, "workspace.stop", w.Name, "")
	return nil
}

// History is what the session has on screen and above it.
//
// The browser's own scrollback starts empty on every attach, so without this
// coming back after a few hours shows a live screen with no idea how it got
// there. tmux has kept it the whole time.
func (s *Service) History(ctx context.Context, actor, id string, lines int) (string, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return "", err
	}
	if lines <= 0 || lines > 20000 {
		lines = 5000
	}
	return s.tmux(ctx, actor, "capture-pane", "-p", "-S", "-"+fmt.Sprint(lines), "-t", SessionName(id))
}

// Touch records that somebody attached, so the list can say how long a
// workspace has been left alone.
func (s *Service) Touch(ctx context.Context, id string) {
	_, _ = s.st.DB.ExecContext(ctx,
		`UPDATE workspaces SET last_attached_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ? AND server_id = ?`,
		id, s.st.ServerID)
}

// Attach makes sure there is something to attach to, and returns the argv.
func (s *Service) Attach(ctx context.Context, actor, id string) (*Workspace, []string, error) {
	w, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if err := s.ensure(ctx, actor, w); err != nil {
		return nil, nil, err
	}
	s.Touch(ctx, id)
	// The workspace's own terminal is its shell window. Landing on whichever
	// window happened to be current means opening a workspace can drop you into
	// an agent's conversation and type into it.
	return w, s.AttachAgentArgv(id, ShellWindow), nil
}

// ---- boot ----------------------------------------------------------------

// Reconcile puts back the sessions a reboot took away.
//
// tmux does not survive a restart of the machine, so after one every workspace
// is gone. Each is recreated at a shell prompt in the right directory, and then
// every agent marked to resume is started again, back in the conversation it
// was in rather than an empty one.
//
// v0.9.0 deliberately did not re-run anything here, on the reasoning that an
// agent resuming mid-task with nobody watching is not a thing to do on
// somebody's behalf. That reasoning survives as the per-agent switch: it is now
// asked for once, per agent, instead of decided for everyone. An agent that was
// never started is still not started — resuming applies to work that was
// already going when the machine went down.
func (s *Service) Reconcile(ctx context.Context) {
	s.EnsureRestartSafe(ctx)
	if !s.HasTmux(ctx) {
		return
	}
	list, err := s.List(ctx)
	if err != nil {
		return
	}
	var back, resumed []string
	for i := range list {
		w := list[i]
		if w.Running {
			continue
		}
		if fi, err := os.Stat(w.Directory); err != nil || !fi.IsDir() {
			continue // the directory went away; leave it alone and let the page say so
		}
		if err := s.ensure(ctx, "system", &w); err != nil {
			s.log.Warn("workspace session could not be recreated", "workspace", w.Name, "err", err)
			continue
		}
		back = append(back, w.Name)
		resumed = append(resumed, s.resumeAgents(ctx, &w)...)
	}
	if len(back) > 0 && s.bus != nil {
		msg := strings.Join(back, ", ") + " came back at a shell prompt."
		if len(resumed) > 0 {
			msg += " Resumed, in the conversations they were already in: " + strings.Join(resumed, ", ") + "."
		} else {
			msg += " No agent was set to resume, so nothing was re-run."
		}
		s.bus.Emit(ctx, notify.Event{
			Category: "system", Severity: "info",
			Title:   "Workspaces are back after a restart",
			Message: msg,
			Link:    "/workspaces",
		})
	}
}

// resumeAgents restarts the agents of one workspace that asked to be resumed,
// and names them so the notification can say what is running again.
//
// Only agents that had been started before: resuming is about picking work back
// up, and an agent that has never run has no conversation to return to.
func (s *Service) resumeAgents(ctx context.Context, w *Workspace) []string {
	agents, err := s.Agents(ctx, w.ID)
	if err != nil {
		return nil
	}
	var names []string
	for i := range agents {
		a := agents[i]
		if !a.Resume || a.LastStarted == "" || a.Running {
			continue
		}
		if err := s.StartAgent(ctx, "system", w.ID, a.ID); err != nil {
			s.log.Warn("agent could not be resumed", "workspace", w.Name, "agent", a.Name, "err", err)
			continue
		}
		names = append(names, w.Name+"/"+a.Name)
	}
	return names
}

// dropIn is what makes a restart survivable, and it is deliberately the
// smallest possible statement of it.
const dropIn = `# Written by isletd. A workspace keeps an agent alive under tmux so that
# restarting the daemon, which is what an update does, cannot end it.
#
# KillMode: the default sends SIGTERM to every process in this unit's control
# group, and tmux is in it because isletd started it.
#
# PrivateTmp: with a /tmp of its own, systemd destroys that /tmp on restart and
# builds another. A session that survives the restart then holds a mount whose
# backing directory is gone, and anything in it that touches /tmp fails with
# ENOENT. The two settings have to change together: KillMode alone turns a
# killed session into a stranded one.
[Service]
KillMode=process
PrivateTmp=no
`

// EnsureRestartSafe repairs the unit on a server that was installed before this
// was understood.
//
// The packaged unit now carries KillMode=process, but an update replaces the
// binary and never the unit. Without this, every machine installed earlier
// would go on killing its own workspaces on the next update - the fault fixed
// here, still present after the fix shipped, on exactly the servers that have
// been running longest. The drop-in is written once and is a no-op afterwards.
//
// A reload does not restart anything; it takes effect at the next stop, which
// is the one that would otherwise have done the damage.
func (s *Service) EnsureRestartSafe(ctx context.Context) {
	unit := false
	for _, p := range []string{"/etc/systemd/system/isletd.service", "/lib/systemd/system/isletd.service", "/usr/lib/systemd/system/isletd.service"} {
		if fileExists(p) {
			unit = true
			break
		}
	}
	if !unit {
		return // a container or a development machine: nothing to repair
	}
	const dir = "/etc/systemd/system/isletd.service.d"
	path := filepath.Join(dir, "10-workspaces.conf")
	if b, err := os.ReadFile(path); err == nil && string(b) == dropIn {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.log.Warn("workspaces: could not write the systemd drop-in", "err", err)
		return
	}
	if err := os.WriteFile(path, []byte(dropIn), 0o644); err != nil {
		s.log.Warn("workspaces: could not write the systemd drop-in", "err", err)
		return
	}
	if _, err := s.run.Run(ctx, "system", "systemctl", "daemon-reload"); err != nil {
		s.log.Warn("workspaces: systemd did not reload; the drop-in applies after the next reload", "err", err)
		return
	}
	s.log.Info("workspaces: restarting isletd will no longer end the sessions it started")
}

// Stranded reports that the tmux server is running in a mount namespace this
// daemon no longer shares.
//
// That happens to a session started while the unit still had PrivateTmp=yes and
// KillMode=process: it survives the restart, systemd destroys the /tmp it was
// holding, and the session goes on running with a /tmp that resolves to
// nothing. Nothing in the session reports it — the shell is fine, the agent is
// fine, and then one command fails with ENOENT on a path that obviously exists.
//
// Restarting the tmux server fixes it, and that ends the work, so it is said
// rather than done.
func (s *Service) Stranded(ctx context.Context) bool {
	out, err := s.tmux(ctx, "system", "display-message", "-p", "#{pid}")
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	mine, err1 := os.Readlink("/proc/self/ns/mnt")
	theirs, err2 := os.Readlink("/proc/" + strings.TrimSpace(out) + "/ns/mnt")
	if err1 != nil || err2 != nil || mine == "" || theirs == "" {
		return false // not Linux, or no /proc: nothing can be concluded
	}
	return mine != theirs
}

// ---- storage -------------------------------------------------------------

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Validate normalises a workspace and explains anything it refuses.
func (w *Workspace) Validate() error {
	w.Name = strings.ToLower(strings.TrimSpace(w.Name))
	w.Directory = strings.TrimSpace(w.Directory)
	w.Command = strings.TrimSpace(w.Command)
	if !nameRe.MatchString(w.Name) {
		return errors.New("the name must be lowercase letters, digits and dashes, and start with a letter or digit")
	}
	if w.Directory == "" {
		return errors.New("give the workspace a directory to work in")
	}
	if !filepath.IsAbs(w.Directory) {
		return errors.New("the directory must be an absolute path")
	}
	fi, err := os.Stat(w.Directory)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory on this server", w.Directory)
	}
	switch w.Preset {
	case "claude":
		if w.Command == "" {
			w.Command = "claude"
		}
	case "shell":
		w.Command = ""
	case "custom":
		if w.Command == "" {
			return errors.New("a custom workspace needs a command to run")
		}
	default:
		return errors.New("preset must be claude, shell or custom")
	}
	// The command is typed into a session with send-keys, one line.
	if strings.ContainsAny(w.Command, "\n\r") {
		return errors.New("the command must be a single line")
	}
	// MCP belongs to the workspace, not to its preset. That rule was written
	// when a workspace was one command and a shell had nothing to configure;
	// now the agents are what run, and they are configured per workspace.
	if w.Preset != "claude" {
		w.SkipPermissions = false // the flag belongs to one program
	}
	return nil
}

const cols = `id, name, directory, preset, command, mcp_enabled, mcp_token_id, skip_permissions, created_at, updated_at, last_attached_at`

func scan(sc interface{ Scan(...any) error }) (Workspace, string, error) {
	var w Workspace
	var mcp, skip int
	var tokenID string
	err := sc.Scan(&w.ID, &w.Name, &w.Directory, &w.Preset, &w.Command, &mcp, &tokenID, &skip,
		&w.CreatedAt, &w.UpdatedAt, &w.LastAttach)
	w.MCPEnabled, w.SkipPermissions = mcp == 1, skip == 1
	return w, tokenID, err
}

// List returns every workspace on this server, with live state from tmux.
func (s *Service) List(ctx context.Context) ([]Workspace, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT `+cols+` FROM workspaces WHERE server_id = ? ORDER BY name`, s.st.ServerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		w, _, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Running, out[i].Started = s.running(ctx, out[i].ID)
	}
	return out, nil
}

// Get reads one, with live state.
func (s *Service) Get(ctx context.Context, id string) (*Workspace, error) {
	row := s.st.DB.QueryRowContext(ctx,
		`SELECT `+cols+` FROM workspaces WHERE id = ? AND server_id = ?`, id, s.st.ServerID)
	w, _, err := scan(row)
	if err != nil {
		return nil, ErrNotFound
	}
	w.Running, w.Started = s.running(ctx, w.ID)
	return &w, nil
}

// TokenID is the API token minted for this workspace's MCP access, if any.
func (s *Service) TokenID(ctx context.Context, id string) string {
	row := s.st.DB.QueryRowContext(ctx,
		`SELECT `+cols+` FROM workspaces WHERE id = ? AND server_id = ?`, id, s.st.ServerID)
	_, tokenID, err := scan(row)
	if err != nil {
		return ""
	}
	return tokenID
}

// Save inserts or updates a workspace.
func (s *Service) Save(ctx context.Context, actor string, w *Workspace) (*Workspace, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}
	mcp, skip := 0, 0
	if w.MCPEnabled {
		mcp = 1
	}
	if w.SkipPermissions {
		skip = 1
	}
	if w.ID == "" {
		w.ID = newID()
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO workspaces
			(id, server_id, name, directory, preset, command, mcp_enabled, skip_permissions)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			w.ID, s.st.ServerID, w.Name, w.Directory, w.Preset, w.Command, mcp, skip)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("there is already a workspace with that name")
			}
			return nil, err
		}
	} else {
		res, err := s.st.DB.ExecContext(ctx, `UPDATE workspaces
			SET name=?, directory=?, preset=?, command=?, mcp_enabled=?, skip_permissions=?,
			    updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id = ? AND server_id = ?`,
			w.Name, w.Directory, w.Preset, w.Command, mcp, skip, w.ID, s.st.ServerID)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("there is already a workspace with that name")
			}
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrNotFound
		}
	}
	_ = s.st.Audit(ctx, actor, "workspace.save", w.Name, w.Directory)
	return s.Get(ctx, w.ID)
}

// SetToken records the API token minted for this workspace's MCP access.
func (s *Service) SetToken(ctx context.Context, id, tokenID string) error {
	_, err := s.st.DB.ExecContext(ctx,
		`UPDATE workspaces SET mcp_token_id = ? WHERE id = ? AND server_id = ?`, tokenID, id, s.st.ServerID)
	return err
}

// Delete kills the session, forgets the workspace and removes its files. The
// caller revokes the API token, which is the one thing this package cannot do
// without importing auth.
func (s *Service) Delete(ctx context.Context, actor, id string) error {
	w, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if ok, _ := s.running(ctx, id); ok {
		_, _ = s.tmux(ctx, actor, "kill-session", "-t", SessionName(id))
	}
	if _, err := s.st.DB.ExecContext(ctx,
		`DELETE FROM workspaces WHERE id = ? AND server_id = ?`, id, s.st.ServerID); err != nil {
		return err
	}
	_ = os.RemoveAll(s.Dir(id))
	_ = s.st.Audit(ctx, actor, "workspace.delete", w.Name, "")
	return nil
}

// ---- MCP -----------------------------------------------------------------

func (s *Service) mcpPath(id string) string { return filepath.Join(s.Dir(id), "mcp.json") }

// MCPPath is where this workspace's MCP configuration lives.
func (s *Service) MCPPath(id string) string { return s.mcpPath(id) }

// WriteMCP points the agent at this panel's own MCP endpoint.
//
// The file is in Islet's data directory rather than the project, and that is
// the whole point of the function. Claude Code also reads .mcp.json from a
// repository root, and a bearer token sitting in a repository root is one
// `git add .` away from being published. Here it is 0600, outside the tree, and
// revoked when the workspace is deleted.
func (s *Service) WriteMCP(id, url, token string) error {
	if err := os.MkdirAll(s.Dir(id), 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf(
		`{"mcpServers":{"islet":{"type":"http","url":%q,"headers":{"Authorization":"Bearer %s"}}}}`+"\n",
		strings.TrimRight(url, "/")+"/mcp", token)
	return os.WriteFile(s.mcpPath(id), []byte(body), 0o600)
}

// ---- helpers -------------------------------------------------------------

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// Since is how long ago a stored timestamp was, for the list.
func Since(ts string) time.Duration {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}
	return time.Since(t)
}
