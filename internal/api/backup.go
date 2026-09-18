package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/isletdev/islet/internal/backup"
	"github.com/isletdev/islet/internal/cmdrun"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) backupErr(w http.ResponseWriter, err error) {
	if errors.Is(err, backup.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, api.Error{Error: "not_found", Message: err.Error()})
		return
	}
	s.failed(w, "backup", err)
}

func redactDest(d *backup.Destination) {
	d.Password = ""
	for k := range d.Config {
		if k == "secretKey" || k == "privateKey" || k == "password" {
			d.Config[k] = ""
		}
	}
}

func (s *Server) handleBackupOverview(w http.ResponseWriter, r *http.Request) {
	dests, err := s.backup.Destinations(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	for i := range dests {
		redactDest(&dests[i])
	}
	plans, _ := s.backup.Plans(r.Context())
	if plans == nil {
		plans = []backup.Plan{}
	}
	kitAt, _, _ := s.store.Setting(r.Context(), "backup.kit_downloaded_at")
	out := map[string]any{"destinations": dests, "plans": plans, "health": s.backup.Health(r.Context()), "volumes": s.backup.VolumeNames(r.Context()), "kitDownloadedAt": kitAt}
	if s.db != nil {
		if inst, err := s.db.List(r.Context(), userFrom(r.Context()).Username); err == nil {
			names := []string{}
			for _, i := range inst {
				names = append(names, i.Name)
			}
			out["databases"] = names
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDestinationSave(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var d backup.Destination
	if err := decode(r, &d); err != nil {
		s.badJSON(w, err)
		return
	}
	d.ID = r.PathValue("id")
	saved, err := s.backup.SaveDestination(r.Context(), userFrom(r.Context()).Username, &d)
	if err != nil {
		s.backupErr(w, err)
		return
	}
	redactDest(saved)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDestinationDelete(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := s.backup.DeleteDestination(r.Context(), r.PathValue("id")); err != nil {
		s.backupErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDestinationVerify(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	out, err := s.backup.Verify(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "check", Message: cmdrun.Redact(err.Error() + "\n" + out)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

func (s *Server) handleDestinationRestoreTest(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	out, err := s.backup.RestoreTest(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "restore_test", Message: cmdrun.Redact(err.Error() + "\n" + out)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.backup.Snapshots(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"), r.URL.Query().Get("plan"))
	if err != nil {
		s.backupErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSnapshotLs(w http.ResponseWriter, r *http.Request) {
	list, err := s.backup.Ls(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"), r.PathValue("snapshot"), r.URL.Query().Get("path"))
	if err != nil {
		s.backupErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	var req struct {
		Snapshot, Include, NewVolume string
		DryRun                       bool
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	target, err := s.backup.Restore(r.Context(), userFrom(r.Context()).Username, r.PathValue("id"), req.Snapshot, req.Include, req.NewVolume, req.DryRun)
	if err != nil {
		s.backupErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"target": target, "dryRun": req.DryRun})
}

func (s *Server) handlePlanSave(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	p := backup.Plan{Enabled: true, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6, KeepYearly: 1}
	if err := decode(r, &p); err != nil {
		s.badJSON(w, err)
		return
	}
	p.ID = r.PathValue("id")
	saved, err := s.backup.SavePlan(r.Context(), userFrom(r.Context()).Username, &p)
	if err != nil {
		s.backupErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handlePlanDelete(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := s.backup.DeletePlan(r.Context(), r.PathValue("id")); err != nil {
		s.backupErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePlanRun(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role == "viewer" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "viewers cannot run backups"})
		return
	}
	id := r.PathValue("id")
	if _, err := s.backup.Plan(r.Context(), id); err != nil {
		s.backupErr(w, err)
		return
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := s.backup.RunPlan(context.Background(), "manual", id, pw)
		pw.Close()
		done <- err
	}()
	streamLines(w, r, pr, func() error { pr.Close(); return <-done })
}

func (s *Server) handlePlanCancel(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if !s.backup.Cancel(r.PathValue("id")) {
		writeJSON(w, http.StatusConflict, api.Error{Error: "idle", Message: "the plan is not running"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePlanRuns(w http.ResponseWriter, r *http.Request) {
	list, err := s.backup.Runs(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePlanRunDetail(w http.ResponseWriter, r *http.Request) {
	rid, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, err := s.backup.RunDetail(r.Context(), r.PathValue("id"), rid)
	if err != nil {
		s.backupErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRecoveryKit(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	kit, err := s.backup.RecoveryKit(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), userFrom(r.Context()).Username, "backup.kit", "", "downloaded")
	_ = s.store.SetSetting(r.Context(), "backup.kit_downloaded_at", time.Now().UTC().Format(time.RFC3339))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"islet-recovery-kit.json\"")
	_, _ = w.Write(kit)
}
