package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/isletdev/islet/internal/cron"
	"github.com/isletdev/islet/internal/db"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) dbErr(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: "database instance not found"})
		return
	}
	writeJSON(w, http.StatusBadRequest, api.Error{Error: "db", Message: err.Error()})
}

// dbInstance loads the instance from the path and enforces admin for writes.
func (s *Server) dbInstance(w http.ResponseWriter, r *http.Request, write bool) *db.Instance {
	u := userFrom(r.Context())
	if write && u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins change databases"})
		return nil
	}
	inst, err := s.db.Get(r.Context(), u.Username, r.PathValue("name"))
	if err != nil {
		s.dbErr(w, err)
		return nil
	}
	return inst
}

func (s *Server) handleDBList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, err := s.db.List(r.Context(), u.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	if u.Role != "admin" {
		for i := range list {
			list[i].Redact()
		}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDBGet(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, false)
	if inst == nil {
		return
	}
	inst.AllowFrom, _, _ = s.store.Setting(r.Context(), "db.allow."+inst.Name)
	u := userFrom(r.Context())
	if u.Role != "admin" {
		inst.Redact()
	}
	out := struct {
		*db.Instance
		Databases  []db.Database  `json:"databases"`
		Stats      *db.Stats      `json:"stats,omitempty"`
		Extensions []db.Extension `json:"extensions"`
		Dumps      []db.Dump      `json:"dumps"`
		Error      string         `json:"error,omitempty"`
		DumpJob    *cron.Job      `json:"dumpJob,omitempty"`
	}{Instance: inst, Databases: []db.Database{}, Extensions: []db.Extension{}, Dumps: []db.Dump{}}
	if inst.State == "running" {
		var err error
		if out.Databases, err = s.db.Databases(r.Context(), u.Username, inst); err != nil {
			out.Error = err.Error()
		}
		if st, err := s.db.Stats(r.Context(), u.Username, inst); err == nil {
			out.Stats = st
		}
		if ext, err := s.db.Extensions(r.Context(), u.Username, inst); err == nil {
			out.Extensions = ext
		}
	}
	out.Dumps, _ = s.db.Dumps(inst)
	if jobs, err := s.cron.List(r.Context()); err == nil {
		for i := range jobs {
			if jobs[i].Name == dumpJobName(inst) {
				out.DumpJob = &jobs[i]
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func dumpJobName(inst *db.Instance) string { return "Dump " + inst.Name }

func (s *Server) handleDBCreate(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	var req struct{ Name, User, Password string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u := userFrom(r.Context())
	url, err := s.db.CreateDatabase(r.Context(), u.Username, inst, req.Name, req.User, req.Password)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "db.create", inst.Name, req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) handleDBDrop(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	u := userFrom(r.Context())
	if err := s.db.DropDatabase(r.Context(), u.Username, inst, r.PathValue("db")); err != nil {
		s.dbErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "db.drop", inst.Name, r.PathValue("db"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDBSlow(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, false)
	if inst == nil {
		return
	}
	list, err := s.db.SlowQueries(r.Context(), userFrom(r.Context()).Username, inst)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDBExtension(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	var req struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u := userFrom(r.Context())
	if err := s.db.SetExtension(r.Context(), u.Username, inst, req.Name, req.Enabled); err != nil {
		s.dbErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "db.extension", inst.Name, req.Name+"="+strconv.FormatBool(req.Enabled))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDBDump(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	var req struct{ Database string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u := userFrom(r.Context())
	d, err := s.db.DumpNow(r.Context(), u.Username, inst, req.Database)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "db.dump", inst.Name, d.File)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDBRestore(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	var req struct{ File, Database string }
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u := userFrom(r.Context())
	if err := s.db.Restore(r.Context(), u.Username, inst, req.File, req.Database); err != nil {
		s.dbErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "db.restore", inst.Name, req.File+" -> "+req.Database)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDBDumpDownload(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	p, err := s.db.DumpPath(inst, r.PathValue("file"))
	if err != nil {
		s.dbErr(w, err)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+r.PathValue("file")+"\"")
	http.ServeFile(w, r, p)
}

func (s *Server) handleDBDumpDelete(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	if err := s.db.DeleteDump(inst, r.PathValue("file")); err != nil {
		s.dbErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDBSchedule creates or updates the cron job that dumps the instance.
func (s *Server) handleDBSchedule(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	var req struct {
		Schedule string `json:"schedule"`
		KeepDays int    `json:"keepDays"`
		Enabled  bool   `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	if req.KeepDays <= 0 {
		req.KeepDays = 14
	}
	if req.Schedule == "" {
		req.Schedule = "0 3 * * *"
	}
	u := userFrom(r.Context())
	job := &cron.Job{Name: dumpJobName(inst), Type: cron.TypeScript, Schedule: req.Schedule, Script: s.db.DumpScript(inst, req.KeepDays), Enabled: req.Enabled, TimeoutSec: 6 * 3600, NotifyOn: "failure"}
	if jobs, err := s.cron.List(r.Context()); err == nil {
		for _, j := range jobs {
			if j.Name == job.Name {
				job.ID = j.ID
				if j.Script != "" && j.Type == cron.TypeScript {
					job.Script = j.Script // keep the user's edits; only schedule and state change
				}
			}
		}
	}
	saved, err := s.cron.Save(r.Context(), u.Username, job)
	if err != nil {
		s.cronErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "db.schedule", inst.Name, req.Schedule)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDBPublic(w http.ResponseWriter, r *http.Request) {
	inst := s.dbInstance(w, r, true)
	if inst == nil {
		return
	}
	var req struct {
		Public    bool   `json:"public"`
		HostPort  int    `json:"hostPort"`
		AllowFrom string `json:"allowFrom"` // comma list of IPs/CIDRs; empty = anyone
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "bad_json", Message: err.Error()})
		return
	}
	u := userFrom(r.Context())
	rc, wait, err := s.db.SetPublic(r.Context(), u.Username, inst, req.Public, req.HostPort)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "db.public", inst.Name, strconv.FormatBool(req.Public))
	// Firewall rules follow the published port: with an allowlist only those
	// sources get through ufw (Docker ports honour it thanks to DOCKER-USER).
	port := req.HostPort
	if port <= 0 {
		port = inst.Port
	}
	prev, _, _ := s.store.Setting(r.Context(), "db.allow."+inst.Name)
	fwNote := ""
	if s.security != nil && s.security.FirewallStatus(r.Context()).Active {
		for _, cidr := range splitList(prev) {
			_ = s.security.DenyPort(r.Context(), u.Username, strconv.Itoa(port), "tcp", cidr)
		}
		if req.Public {
			list := splitList(req.AllowFrom)
			for _, cidr := range list {
				if err := s.security.AllowPort(r.Context(), u.Username, strconv.Itoa(port), "tcp", cidr, "db "+inst.Name); err != nil {
					fwNote = "firewall rule failed: " + err.Error()
				}
			}
			if len(list) == 0 {
				if err := s.security.AllowPort(r.Context(), u.Username, strconv.Itoa(port), "tcp", "", "db "+inst.Name); err != nil {
					fwNote = "firewall rule failed: " + err.Error()
				}
			} else {
				_ = s.security.DenyPort(r.Context(), u.Username, strconv.Itoa(port), "tcp", "")
			}
		} else {
			_ = s.security.DenyPort(r.Context(), u.Username, strconv.Itoa(port), "tcp", "")
		}
	} else if req.Public && strings.TrimSpace(req.AllowFrom) != "" {
		fwNote = "the firewall is not active, so the allowlist is recorded but not enforced; enable ufw on the Security page"
	}
	if req.Public {
		_ = s.store.SetSetting(r.Context(), "db.allow."+inst.Name, strings.Join(splitList(req.AllowFrom), ","))
	} else {
		_ = s.store.SetSetting(r.Context(), "db.allow."+inst.Name, "")
	}
	if fwNote != "" {
		pr, pw := io.Pipe()
		go func() {
			defer pw.Close()
			_, _ = io.Copy(pw, rc)
			fmt.Fprintln(pw, "[islet] "+fwNote)
		}()
		streamLines(w, r, pr, wait)
		return
	}
	streamLines(w, r, rc, wait)
}

// splitList splits a comma or whitespace separated list, dropping blanks.
func splitList(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
