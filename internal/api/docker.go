package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/isletdev/islet/internal/docker"
	"github.com/isletdev/islet/internal/proxy"
	"github.com/isletdev/islet/internal/terminal"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) dockerErr(w http.ResponseWriter, err error) {
	if err == docker.ErrBadName {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "invalid name"})
		return
	}
	// A container that is not there is not a broken Docker. Answering 502 for
	// it told every client — the panel, a script, an agent through MCP — that
	// the daemon had failed, when the only thing wrong was the name.
	if docker.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no such container, image, volume or network"})
		return
	}
	writeJSON(w, http.StatusBadGateway, api.Error{Error: "docker", Message: err.Error()})
}

func (s *Server) handleDockerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.docker.Status(r.Context()))
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, err := s.docker.Containers(r.Context(), u.Username)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	if scoped(u) {
		kept := list[:0]
		for _, c := range list {
			if s.allowsContainer(r.Context(), u, c.Name) {
				kept = append(kept, c)
			}
		}
		list = kept
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleContainer(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	c, err := s.docker.Inspect(r.Context(), u.Username, r.PathValue("id"))
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	if u.Role != "admin" {
		for i, e := range c.Env {
			if k, _, ok := strings.Cut(e, "="); ok {
				c.Env[i] = k + "=••••••"
			}
		}
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	action := r.PathValue("action")
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot change containers"})
		return
	}
	if err := s.docker.ContainerAction(r.Context(), u.Username, r.PathValue("id"), action); err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "container."+action, r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleContainerLimits(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can change limits"})
		return
	}
	var req struct {
		MemoryBytes int64   `json:"memoryBytes"`
		CPUs        float64 `json:"cpus"`
		Restart     string  `json:"restart"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if err := s.docker.UpdateLimits(r.Context(), u.Username, r.PathValue("id"), req.MemoryBytes, req.CPUs, req.Restart); err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "container.limits", r.PathValue("id"), fmt.Sprintf("mem=%d cpus=%g restart=%s", req.MemoryBytes, req.CPUs, req.Restart))
	w.WriteHeader(http.StatusNoContent)
}

// handleContainerLogs streams log lines as SSE. ?tail=200&follow=1
func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	tail := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("tail")); err == nil && v >= 0 && v <= 10000 {
		tail = v
	}
	follow := r.URL.Query().Get("follow") == "1"
	rc, wait, err := s.docker.Logs(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"), tail, follow)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	streamLines(w, r, rc, wait)
}

// handleStackImport adopts a Compose project Docker already knows by
// copying its file into the managed directory.
func (s *Server) handleStackImport(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins import stacks"})
		return
	}
	var req struct{ Name string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	stacks, err := s.docker.Stacks(r.Context(), u.Username)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	for _, st := range stacks {
		if st.Name != req.Name {
			continue
		}
		if st.Managed {
			writeJSON(w, http.StatusConflict, api.Error{Error: "managed", Message: "this stack is already managed"})
			return
		}
		path := strings.Split(st.Path, ",")[0]
		compose, err := os.ReadFile(path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, api.Error{Error: "read", Message: "cannot read " + path + ": " + err.Error()})
			return
		}
		env, _ := os.ReadFile(filepath.Join(filepath.Dir(path), ".env"))
		if err := s.docker.WriteStack(r.Context(), u.Username, st.Name, string(compose), string(env)); err != nil {
			s.dockerErr(w, err)
			return
		}
		_ = s.store.Audit(r.Context(), u.Username, "stack.import", st.Name, path)
		// Domains the stack already serves through Traefik labels become
		// Islet domains pointing at the same containers.
		var added []string
		if routes := docker.ExtractRoutes(string(compose)); len(routes) > 0 && s.proxy != nil {
			existing := map[string]bool{}
			if doms, err := s.proxy.Domains(r.Context()); err == nil {
				for _, d := range doms {
					existing[d.Host+d.PathPrefix] = true
				}
			}
			for _, rt := range routes {
				if existing[rt.Host+rt.Prefix] {
					continue
				}
				container := st.Name + "-" + rt.Service + "-1"
				_ = s.proxy.Connect(r.Context(), u.Username, container)
				tls := "letsencrypt"
				if !rt.TLS {
					tls = "none"
				}
				d := &proxy.Domain{Host: rt.Host, PathPrefix: rt.Prefix, TargetType: "container", Target: container, Port: rt.Port, TLS: tls, Enabled: true}
				if _, err := s.proxy.Save(r.Context(), u.Username, d); err == nil {
					added = append(added, rt.Host+rt.Prefix)
				}
			}
		}
		note := "Imported. The running containers keep working; the next up/down from Islet uses the managed copy, so remove the old file from your own automation."
		if len(added) > 0 {
			note += " Domains found in Traefik labels were added: " + strings.Join(added, ", ") + "; check their TLS setting."
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": st.Name, "from": path, "note": note, "domains": added})
		return
	}
	writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "no Compose project with that name"})
}

// streamLines copies a line reader to the client as SSE "line" events.
//
// It takes an io.ReadCloser and always closes it. Taking a bare reader meant
// that when a client disconnected mid-stream the loop ended but the producer
// stayed blocked writing into a pipe nobody was reading, so the command was
// never reaped and the goroutine never returned.
func streamLines(w http.ResponseWriter, r *http.Request, rc io.ReadCloser, wait func() error) {
	defer rc.Close()
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: "streaming unsupported"})
		return
	}
	// Drain the request body first: closing a connection with unread request
	// bytes makes the kernel send RST, and Windows clients then drop the
	// buffered tail of the response.
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	// no-transform is the part that is not about caching: it tells a CDN or a
	// proxy not to recompress this, and a compressor in front of a stream holds
	// its first kilobyte back — which for a stream is however long the work
	// takes. Traefik's own compressor is told the same thing by content type,
	// in the middleware Islet writes for it.
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		b, _ := json.Marshal(sc.Text())
		if _, err := fmt.Fprintf(w, "event: line\ndata: %s\n\n", b); err != nil {
			break
		}
		fl.Flush()
	}
	err := wait()
	msg := "done"
	if err != nil && r.Context().Err() == nil {
		msg = "error: " + err.Error()
	}
	fmt.Fprintf(w, "event: end\ndata: %q\n\n", msg)
	fl.Flush()
}

func (s *Server) handleContainerExec(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can open a container shell"})
		return
	}
	argv, err := docker.ExecCommand(r.PathValue("id"))
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	s.servePTY(w, r, terminal.Options{Command: argv}, "container.exec", r.PathValue("id"))
}

// servePTY is shared by the host terminal and container exec.
func (s *Server) servePTY(w http.ResponseWriter, r *http.Request, opts terminal.Options, auditAction, target string) {
	u := userFrom(r.Context())
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "closed")
	conn.SetReadLimit(1 << 20)
	ctx, cancel := contextWithCancel(r)
	defer cancel()
	sess, err := terminal.Start(opts)
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageBinary, []byte("\r\nislet: cannot start: "+err.Error()+"\r\n"))
		conn.Close(websocket.StatusInternalError, "start failed")
		return
	}
	defer sess.Close()
	_ = s.store.Audit(ctx, u.Username, auditAction+".open", target, "ip="+clientIP(r))
	// A terminal that is being read rather than typed into sends nothing for
	// minutes at a time, and an idle WebSocket is exactly what a reverse proxy
	// closes. The SSE endpoints already keep themselves alive this way; without
	// it, a workspace left open while something long runs drops for no reason
	// the person can see.
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := contextWithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				wctx, wcancel := contextWithTimeout(ctx, 10*time.Second)
				werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		switch typ {
		case websocket.MessageBinary:
			if _, err := sess.Write(data); err != nil {
				break
			}
		case websocket.MessageText:
			var c termControl
			if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols > 0 && c.Rows > 0 && c.Cols <= 500 && c.Rows <= 200 {
				_ = sess.Resize(c.Cols, c.Rows)
			}
		}
	}
	_ = s.store.Audit(contextBackground(), u.Username, auditAction+".close", target, "")
	conn.Close(websocket.StatusNormalClosure, "bye")
}

// ---- images, volumes, networks ----

func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	list, err := s.docker.Images(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleImagePull(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot pull images"})
		return
	}
	ref := r.URL.Query().Get("ref")
	rc, wait, err := s.docker.Pull(r.Context(), u.Username, ref)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "image.pull", ref, "")
	streamLines(w, r, rc, wait)
}

func (s *Server) handleImageRemove(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot remove images"})
		return
	}
	if err := s.docker.RemoveImage(r.Context(), u.Username, r.PathValue("id"), r.URL.Query().Get("force") == "1"); err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "image.remove", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVolumes(w http.ResponseWriter, r *http.Request) {
	list, err := s.docker.Volumes(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleVolumeRemove(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can delete volumes"})
		return
	}
	if err := s.docker.RemoveVolume(r.Context(), u.Username, r.PathValue("name")); err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "volume.remove", r.PathValue("name"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	list, err := s.docker.Networks(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleNetworkRemove(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can delete networks"})
		return
	}
	if err := s.docker.RemoveNetwork(r.Context(), u.Username, r.PathValue("name")); err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "network.remove", r.PathValue("name"), "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- disk usage and prune ----

func (s *Server) handleDockerDF(w http.ResponseWriter, r *http.Request) {
	df, err := s.docker.SystemDF(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, df)
}

func (s *Server) handleDockerPrune(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can prune"})
		return
	}
	var o docker.PruneOptions
	if err := decode(r, &o); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	res, err := s.docker.Prune(r.Context(), u.Username, o)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "docker.prune", "", fmt.Sprintf("%v", res))
	writeJSON(w, http.StatusOK, res)
}

// ---- stacks ----

func (s *Server) handleStacks(w http.ResponseWriter, r *http.Request) {
	list, err := s.docker.Stacks(r.Context(), userFrom(r.Context()).Username)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleStack(w http.ResponseWriter, r *http.Request) {
	compose, env, err := s.docker.ReadStack(r.PathValue("name"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "stack is not managed by Islet"})
		return
	}
	if userFrom(r.Context()).Role != "admin" {
		env = ""
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": r.PathValue("name"), "compose": compose, "env": env})
}

// Writing a stack is equivalent to root on the host: a compose file may mount
// the root filesystem or ask for a privileged container. Deployers have neither
// the terminal nor the file writer, so they do not get this either.
func (s *Server) handleStackWrite(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	var req struct {
		Name    string `json:"name"`
		Compose string `json:"compose"`
		Env     string `json:"env"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if name := r.PathValue("name"); name != "" {
		req.Name = name
	}
	if err := s.docker.WriteStack(r.Context(), u.Username, req.Name, req.Compose, req.Env); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "stack.write", req.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"name": req.Name})
}

func (s *Server) handleStackAction(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	u := userFrom(r.Context())
	name, action := r.PathValue("name"), r.PathValue("action")
	rc, wait, err := s.docker.StackAction(r.Context(), u.Username, name, action)
	if err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "stack."+action, name, "")
	streamLines(w, r, rc, wait)
}

func (s *Server) handleStackRemove(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins can remove stacks"})
		return
	}
	name := r.PathValue("name")
	if err := s.docker.RemoveStack(r.Context(), u.Username, name, r.URL.Query().Get("volumes") == "1"); err != nil {
		s.dockerErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "stack.remove", name, "volumes="+r.URL.Query().Get("volumes"))
	w.WriteHeader(http.StatusNoContent)
}

// ---- command transparency ----

func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	// The drawer shows every command Islet ran, including the arguments. Those
	// are redacted, but the list still describes the whole server, so it is an
	// admin view rather than something every signed-in user may read.
	if !s.adminOnly(w, r) {
		return
	}
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id, actor, command, exit_code, duration_ms, stderr, created_at
		FROM commands WHERE server_id = ? ORDER BY id DESC LIMIT ?`, s.store.ServerID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	defer rows.Close()
	out := []api.CommandEntry{}
	for rows.Next() {
		var e api.CommandEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Command, &e.ExitCode, &e.DurationMs, &e.Stderr, &e.CreatedAt); err == nil {
			out = append(out, e)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
