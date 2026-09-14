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
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	LastAttach string `json:"lastAttachedAt"`

	// Derived from tmux, never stored.
	Running bool   `json:"running"`
	Started string `json:"started,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Service owns the workspaces table and the tmux sessions behind it.
type Service struct {
	st  *store.Store
	run *cmdrun.Runner
	bus *notify.Bus
	log *slog.Logger
	dir string // <dataDir>/workspaces
}

// New builds the service. dataDir is the daemon's data directory; per-workspace
// files (an MCP config, for now) live under <dataDir>/workspaces/<id>.
func New(st *store.Store, run *cmdrun.Runner, bus *notify.Bus, dataDir string, log *slog.Logger) *Service {
	return &Service{st: st, run: run, bus: bus, log: log, dir: filepath.Join(dataDir, "workspaces")}
}

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

func (s *Service) tmux(ctx context.Context, actor string, args ...string) (string, error) {
	res, err := s.run.Run(ctx, actor, "tmux", args...)
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
	_, err := s.tmux(ctx, actor, "new-session", "-d", "-s", SessionName(w.ID), "-c", w.Directory)
	return err
}

// AttachArgv is the command that joins a session from a PTY.
//
// -d detaches every other client first. tmux sizes a session to its smallest
// attached client, so a tab left open on a phone would otherwise squeeze a
// desktop session down to its width. The most recent viewer wins.
func AttachArgv(id string) []string {
	return []string{"tmux", "attach-session", "-d", "-t", SessionName(id)}
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
	cmd := w.Command
	if w.Preset == "claude" && w.MCPEnabled {
		if p := s.mcpPath(w.ID); fileExists(p) {
			cmd = cmd + " --mcp-config " + p
		}
	}
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
	return w, AttachArgv(id), nil
}

// ---- boot ----------------------------------------------------------------

// Reconcile puts back the sessions a reboot took away.
//
// tmux does not survive a restart of the machine, so after one every workspace
// is gone. They are recreated at a shell prompt in the right directory, and
// their commands are deliberately not re-run: an agent restarting by itself,
// mid-task, with nobody watching, is not a thing to do on somebody's behalf.
func (s *Service) Reconcile(ctx context.Context) {
	if !s.HasTmux(ctx) {
		return
	}
	list, err := s.List(ctx)
	if err != nil {
		return
	}
	var back []string
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
	}
	if len(back) > 0 && s.bus != nil {
		s.bus.Emit(ctx, notify.Event{
			Category: "system", Severity: "info",
			Title:   "Workspaces are back after a restart",
			Message: strings.Join(back, ", ") + " were recreated at a shell prompt. Nothing was re-run; open one and start it when you are ready.",
			Link:    "/workspaces",
		})
	}
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
	if w.Preset == "shell" {
		w.MCPEnabled = false // nothing is being launched for it to configure
	}
	return nil
}

const cols = `id, name, directory, preset, command, mcp_enabled, mcp_token_id, created_at, updated_at, last_attached_at`

func scan(sc interface{ Scan(...any) error }) (Workspace, string, error) {
	var w Workspace
	var mcp int
	var tokenID string
	err := sc.Scan(&w.ID, &w.Name, &w.Directory, &w.Preset, &w.Command, &mcp, &tokenID,
		&w.CreatedAt, &w.UpdatedAt, &w.LastAttach)
	w.MCPEnabled = mcp == 1
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
	mcp := 0
	if w.MCPEnabled {
		mcp = 1
	}
	if w.ID == "" {
		w.ID = newID()
		_, err := s.st.DB.ExecContext(ctx, `INSERT INTO workspaces
			(id, server_id, name, directory, preset, command, mcp_enabled) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			w.ID, s.st.ServerID, w.Name, w.Directory, w.Preset, w.Command, mcp)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return nil, errors.New("there is already a workspace with that name")
			}
			return nil, err
		}
	} else {
		res, err := s.st.DB.ExecContext(ctx, `UPDATE workspaces
			SET name=?, directory=?, preset=?, command=?, mcp_enabled=?,
			    updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id = ? AND server_id = ?`,
			w.Name, w.Directory, w.Preset, w.Command, mcp, w.ID, s.st.ServerID)
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
