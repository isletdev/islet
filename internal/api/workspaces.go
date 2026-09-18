package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/isletdev/islet/internal/ai"
	"github.com/isletdev/islet/internal/terminal"
	"github.com/isletdev/islet/internal/workspace"
	"github.com/isletdev/islet/pkg/api"
)

// Workspaces give an agent — or any long job — a session that outlives the
// browser. Admin only, for the same reason the terminal is: attaching to one is
// a shell on this server.

func (s *Server) workspaceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
	case errors.Is(err, workspace.ErrNoTmux):
		writeJSON(w, http.StatusPreconditionFailed, api.Error{Error: "no_tmux",
			Message: "tmux is not installed on this server, and a workspace is a tmux session. Install it from the Workspaces page."})
	default:
		s.failed(w, "invalid", err)
	}
}

// admin returns false and answers the request when the caller is not an admin.
func (s *Server) workspaceAdmin(w http.ResponseWriter, r *http.Request) bool {
	if userFrom(r.Context()).Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden",
			Message: "only admins can use workspaces: attaching to one is a shell on this server"})
		return false
	}
	return true
}

func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		list, err := s.workspaces.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
			return
		}
		claude := s.workspaces.ClaudePath(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{
			"workspaces": list,
			"tmux":       s.workspaces.HasTmux(r.Context()),
			"claude":     claude != "",
			"claudePath": claude,
			// Sessions left over from a configuration that has since been
			// repaired. They run, and their /tmp does not exist.
			"stranded": s.workspaces.Stranded(r.Context()),
		})
		return
	}
	var in workspace.Workspace
	if err := decode(r, &in); err != nil {
		s.badJSON(w, err)
		return
	}
	in.ID = ""
	s.saveWorkspace(w, r, &in)
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		ws, err := s.workspaces.Get(r.Context(), id)
		if err != nil {
			s.workspaceErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ws)
	case http.MethodPut:
		var in workspace.Workspace
		if err := decode(r, &in); err != nil {
			s.badJSON(w, err)
			return
		}
		in.ID = id
		s.saveWorkspace(w, r, &in)
	case http.MethodDelete:
		u := userFrom(r.Context())
		// The token goes first: a workspace that is gone from the table but
		// whose token still works is a credential nobody can see to revoke.
		if tid := s.workspaces.TokenID(r.Context(), id); tid != "" {
			_ = s.auth.RevokeToken(r.Context(), u.ID, tid, true)
		}
		if err := s.workspaces.Delete(r.Context(), u.Username, id); err != nil {
			s.workspaceErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// saveWorkspace writes the row and, when the workspace wants it, mints the
// token and writes the MCP configuration that points the agent back at Islet.
func (s *Server) saveWorkspace(w http.ResponseWriter, r *http.Request, in *workspace.Workspace) {
	u := userFrom(r.Context())
	saved, err := s.workspaces.Save(r.Context(), u.Username, in)
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	if saved.MCPEnabled {
		if err := s.wireMCP(r, saved.ID, saved.Name); err != nil {
			// The workspace is real and usable; only the agent's shortcut to
			// Islet is missing, so that is what the message says.
			saved.Error = "the workspace was saved, but its Islet access could not be set up: " + err.Error()
		}
	}
	writeJSON(w, http.StatusOK, saved)
}

// wireMCP gives one workspace a scoped token and an MCP config file.
//
// The scopes are the ones an agent needs to see what it just changed: read
// state, read logs, restart a container, say something happened. Withholding
// "shell" is what this list is usually described by, and on its own that was
// not the boundary it sounded like — the token's owner is the admin who made
// the workspace, so every adminOnly check it met was already satisfied, and
// two of the scopes reached a root shell by another door. "cron" wrote a job,
// and a job is a command run as root on a schedule. "containers" wrote a
// Compose file, and one line of a Compose file is the host filesystem.
//
// So cron is gone from this list, and writing a stack now needs "system",
// which nothing here has. What is left really is what it says: look at the
// server, restart something, report back. The token still belongs to an admin
// and still does not expire, so this is a smaller hole rather than no hole —
// docs/WORKSPACES.md says so plainly rather than claiming a sandbox.
func (s *Server) wireMCP(r *http.Request, id, name string) error {
	u := userFrom(r.Context())
	if tid := s.workspaces.TokenID(r.Context(), id); tid != "" {
		_ = s.auth.RevokeToken(r.Context(), u.ID, tid, true)
	}
	secret, tok, err := s.auth.CreateToken(r.Context(), u.ID, "workspace "+name, "read,logs,containers,notify", 0)
	if err != nil {
		return err
	}
	if err := s.workspaces.SetToken(r.Context(), id, tok.ID); err != nil {
		return err
	}
	return s.workspaces.WriteMCP(id, s.publicURL(r), secret)
}

// publicURL is this panel as the agent on this machine can reach it. The agent
// is local, so the loopback address always works and does not depend on a
// domain being routed yet.
// publicURL is an address for this panel that the thing being told about it can
// actually reach.
//
// A domain routed to the panel comes first, because an agent started here runs
// on the server and the Host header is whatever the person's browser used —
// which may be a local alias, or a name only their machine resolves. That was
// not theoretical: a workspace created through such a name was handed
// "https://islet/mcp", and every MCP call the agent made failed to resolve, so
// it ran without any of Islet's tools and nothing said why.
//
// The request's own host is the fallback, since on a server with no panel
// domain it is the only address there is.
func (s *Server) publicURL(r *http.Request) string {
	if s.proxy != nil {
		if h := s.proxy.PanelHost(r.Context()); h != "" {
			return "https://" + h
		}
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

func (s *Server) handleWorkspaceStart(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	if err := s.workspaces.Start(r.Context(), userFrom(r.Context()).Username, r.PathValue("id")); err != nil {
		s.workspaceErr(w, err)
		return
	}
	ws, err := s.workspaces.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (s *Server) handleWorkspaceStop(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	if err := s.workspaces.Stop(r.Context(), userFrom(r.Context()).Username, r.PathValue("id")); err != nil {
		s.workspaceErr(w, err)
		return
	}
	ws, err := s.workspaces.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// handleWorkspaceHistory returns what the session has scrolled past.
func (s *Server) handleWorkspaceHistory(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	out, err := s.workspaces.History(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"), lines)
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": out})
}

// handleWorkspaceTmux installs tmux, streaming the package manager's output.
func (s *Server) handleWorkspaceTmux(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	rc, wait, err := s.workspaces.InstallTmux(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: err.Error()})
		return
	}
	streamLines(w, r, rc, wait)
}

// handleWorkspaceClaude installs Claude Code, streaming the installer.
func (s *Server) handleWorkspaceClaude(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	rc, wait, err := s.workspaces.InstallClaude(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "install", Message: err.Error()})
		return
	}
	streamLines(w, r, rc, wait)
}

// handleWorkspaceAttach joins the tmux session over a PTY.
//
// Everything that makes this safe and workable — the audit entry, resize
// handling, and the WebSocket splicing that lets it work against another server
// in the fleet — already lives in servePTY. The only new part is which command
// the PTY runs.
func (s *Server) handleWorkspaceAttach(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	u := userFrom(r.Context())
	ws, argv, err := s.workspaces.Attach(r.Context(), u.Username, r.PathValue("id"))
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	// The seat is the tmux session, not the route: a workspace and any of its
	// agent windows are one session, and attaching to any of them detaches the
	// rest whatever the URL said.
	s.servePTYSeat(w, r, terminal.Options{Command: argv, Dir: ws.Directory}, "workspace", ws.Name, "ws:"+ws.ID)
}

// workspaceMCPPath is used by the page to show where the agent's credentials
// live, without ever returning the credential itself.
func (s *Server) handleWorkspaceMCP(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	ws, err := s.workspaces.Get(r.Context(), id)
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	if r.Method == http.MethodPost {
		if err := s.wireMCP(r, id, ws.Name); err != nil {
			writeJSON(w, http.StatusBadGateway, api.Error{Error: "mcp", Message: err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": ws.MCPEnabled,
		"path":    s.workspaces.MCPPath(id),
		"tools":   len(s.mcpTools()),
		"scopes":  strings.Split("read,logs,containers,cron,notify", ","),
	})
}

// ---- agents ---------------------------------------------------------------
//
// A workspace holds several of them, each a tmux window. The handlers are thin
// for the same reason the workspace ones are: the service owns the tmux and the
// audit, and servePTY owns everything that makes a terminal work.

func (s *Server) handleWorkspaceAgents(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodPost {
		var in workspace.Agent
		if err := decode(r, &in); err != nil {
			s.badJSON(w, err)
			return
		}
		in.ID = ""
		if !s.fillAgentModel(w, r, &in) {
			return
		}
		a, err := s.workspaces.SaveAgent(r.Context(), userFrom(r.Context()).Username, id, &in)
		if err != nil {
			s.workspaceErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, a)
		return
	}
	list, err := s.workspaces.Agents(r.Context(), id)
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// fillAgentModel gives an agent the model it will run, and the command that
// runs it. It answers false when it has already written an error.
//
// An agent that names no provider used to be left exactly as it arrived, which
// sounds harmless and was not: the panel hides the model picker when only one
// model is configured — the whole point of configuring one — so every agent
// created on a single-model server arrived with no provider at all. It then ran
// whatever command the form defaulted to, which was the bare word "claude",
// found nothing by that name on PATH inside tmux, and died. The workspace that
// already worked was the one whose command had been filled in by hand, months
// ago, with an absolute path.
//
// So an empty provider resolves to the default, exactly as it does for an
// assistant conversation. That is what "the models are set up once and chosen
// where the work starts" has to mean for this page too: a workspace and the
// assistant read the same list, and neither asks again when there is nothing to
// ask about.
func (s *Server) fillAgentModel(w http.ResponseWriter, r *http.Request, in *workspace.Agent) bool {
	if s.ai == nil || in.Preset == "shell" {
		return true
	}
	p, err := s.ai.Resolve(r.Context(), in.ProviderID)
	if err != nil || p == nil {
		// No models configured at all. The command the caller sent is all there
		// is, and the workspace page says what is missing.
		return true
	}
	if in.ProviderID != "" && p.ID != in.ProviderID {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "no such model"})
		return false
	}
	in.ProviderID = p.ID
	cmd := s.agentCommand(r.Context(), p)
	if cmd == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid",
			Message: p.Name + " is an API endpoint with no command to run in a terminal. It can hold a conversation in the assistant; a workspace agent needs Claude Code, through a subscription or an Anthropic key."})
		return false
	}
	// A command somebody typed is left alone: it has always been what actually
	// runs, and a person who wrote one meant it.
	if strings.TrimSpace(in.Command) == "" || in.Command == "claude" {
		in.Command, in.Preset = cmd, "claude"
	}
	return true
}

// agentCommand is AgentCommand with the installed path filled in.
//
// "claude" on its own is only a command if it happens to be on PATH, and the
// PATH a tmux window inherits is not the one that installed it — Claude Code
// puts itself in ~/.local/bin, which a non-login shell may not have. The
// assistant already resolves this for a subscription; an agent needs the same
// answer or it starts with "command not found".
func (s *Server) agentCommand(ctx context.Context, p *ai.Provider) string {
	cmd := AgentCommand(p)
	if cmd == "claude" && s.workspaces != nil {
		if path := s.workspaces.ClaudePath(ctx); path != "" {
			return path
		}
	}
	return cmd
}

func (s *Server) handleWorkspaceAgent(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	id, agentID := r.PathValue("id"), r.PathValue("agentId")
	user := userFrom(r.Context()).Username
	switch r.Method {
	case http.MethodPut:
		var in workspace.Agent
		if err := decode(r, &in); err != nil {
			s.badJSON(w, err)
			return
		}
		in.ID = agentID
		if !s.fillAgentModel(w, r, &in) {
			return
		}
		a, err := s.workspaces.SaveAgent(r.Context(), user, id, &in)
		if err != nil {
			s.workspaceErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	case http.MethodDelete:
		if err := s.workspaces.DeleteAgent(r.Context(), user, id, agentID); err != nil {
			s.workspaceErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		a, err := s.workspaces.GetAgent(r.Context(), id, agentID)
		if err != nil {
			s.workspaceErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

func (s *Server) handleWorkspaceAgentStart(w http.ResponseWriter, r *http.Request) {
	s.agentAction(w, r, s.workspaces.StartAgent)
}

func (s *Server) handleWorkspaceAgentStop(w http.ResponseWriter, r *http.Request) {
	s.agentAction(w, r, s.workspaces.StopAgent)
}

// agentAction runs one of the two verbs and answers with the agent's new state,
// so the page never has to ask a second time to find out what happened.
func (s *Server) agentAction(w http.ResponseWriter, r *http.Request, do func(context.Context, string, string, string) error) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	id, agentID := r.PathValue("id"), r.PathValue("agentId")
	if err := do(r.Context(), userFrom(r.Context()).Username, id, agentID); err != nil {
		s.workspaceErr(w, err)
		return
	}
	list, err := s.workspaces.Agents(r.Context(), id)
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	for i := range list {
		if list[i].ID == agentID {
			writeJSON(w, http.StatusOK, list[i])
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (s *Server) handleWorkspaceAgentHistory(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	out, err := s.workspaces.AgentHistory(r.Context(), userFrom(r.Context()).Username,
		r.PathValue("id"), r.PathValue("agentId"), lines)
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": out})
}

// handleWorkspaceAgentAttach joins the agent's own window.
//
// The route ends in /attach so it needs the shell scope, like every other
// terminal in the panel — a read-only token must not reach a running agent.
func (s *Server) handleWorkspaceAgentAttach(w http.ResponseWriter, r *http.Request) {
	if !s.workspaceAdmin(w, r) {
		return
	}
	u := userFrom(r.Context())
	id, agentID := r.PathValue("id"), r.PathValue("agentId")
	ws, err := s.workspaces.Get(r.Context(), id)
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	a, err := s.workspaces.GetAgent(r.Context(), id, agentID)
	if err != nil {
		s.workspaceErr(w, err)
		return
	}
	if err := s.workspaces.EnsureAgentWindow(r.Context(), u.Username, id, agentID); err != nil {
		s.workspaceErr(w, err)
		return
	}
	s.workspaces.Touch(r.Context(), id)
	s.servePTYSeat(w, r, terminal.Options{Command: s.workspaces.AttachAgentArgv(id, a.Name), Dir: ws.Directory},
		"workspace", ws.Name+"/"+a.Name, "ws:"+id)
}
