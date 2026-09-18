package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/isletdev/islet/internal/notify"
	"github.com/isletdev/islet/pkg/api"
)

func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	admin := userFrom(r.Context()).Role == "admin"
	list, err := s.notify.Channels(r.Context(), admin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleChannelSave(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins manage channels"})
		return
	}
	var c notify.Channel
	if err := decode(r, &c); err != nil {
		s.badJSON(w, err)
		return
	}
	if id := r.PathValue("id"); id != "" {
		c.ID = id
		// Keep stored secrets when the client omits them (edits without re-entering tokens).
		if old, err := s.notify.Channel(r.Context(), id, true); err == nil {
			for k, v := range old.Config {
				if c.Config[k] == "" {
					if c.Config == nil {
						c.Config = map[string]string{}
					}
					c.Config[k] = v
				}
			}
		}
	} else {
		c.ID = ""
	}
	saved, err := s.notify.SaveChannel(r.Context(), &c)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "channel.save", saved.ID, saved.Type+" "+saved.Name)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleChannelDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u.Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins manage channels"})
		return
	}
	if err := s.notify.DeleteChannel(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	_ = s.store.Audit(r.Context(), u.Username, "channel.delete", r.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChannelTest(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins manage channels"})
		return
	}
	if err := s.notify.Test(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "send_failed", Message: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTelegramDetect(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()).Role != "admin" {
		writeJSON(w, http.StatusForbidden, api.Error{Error: "forbidden", Message: "only admins manage channels"})
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decode(r, &req); err != nil {
		s.badJSON(w, err)
		return
	}
	chats, err := notify.TelegramDetect(r.Context(), req.Token)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, api.Error{Error: "telegram", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, chats)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	list, err := s.notify.Events(r.Context(), limit, before)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleEventDeliveries(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	list, err := s.notify.Deliveries(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleEmit lets scripts and the CLI raise custom events: islet notify "text".
func (s *Server) handleEmit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Severity string `json:"severity"`
		Title    string `json:"title"`
		Message  string `json:"message"`
		Subject  string `json:"subject"`
	}
	if err := decode(r, &req); err != nil || req.Title == "" {
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "title is required"})
		return
	}
	if req.Severity == "" {
		req.Severity = notify.Info
	}
	s.notify.Emit(r.Context(), notify.Event{Category: "custom", Severity: req.Severity, Title: req.Title, Message: req.Message, Subject: strings.TrimSpace(req.Subject), Link: "/notifications"})
	w.WriteHeader(http.StatusAccepted)
}
