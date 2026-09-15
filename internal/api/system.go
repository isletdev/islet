package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/isletdev/islet/internal/metrics"
	"github.com/isletdev/islet/pkg/api"
)

type (
	metricsPortAlias  = metrics.Port
	metricsPointAlias = metrics.Point
)

// publishedRe matches "0.0.0.0:8080->80/tcp" in docker ps port strings.
var publishedRe = regexp.MustCompile(`:(\d+)->\d+/(?:tcp|udp)`)

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.metrics.Host(r.Context()))
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	limit := 15
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	procs, err := s.metrics.TopProcesses(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, procs)
}

func (s *Server) handlePorts(w http.ResponseWriter, r *http.Request) {
	ports, err := s.metrics.ListeningPorts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	if ports == nil {
		ports = []metricsPortAlias{}
	}
	// Published container ports show the container instead of docker-proxy.
	type portRow struct {
		metricsPortAlias
		Container string `json:"container,omitempty"`
	}
	byPort := map[uint32]string{}
	if s.docker != nil {
		if list, err := s.docker.Containers(r.Context(), userFrom(r.Context()).Username); err == nil {
			for _, c := range list {
				for _, m := range publishedRe.FindAllStringSubmatch(c.Ports, -1) {
					if p, err := strconv.Atoi(m[1]); err == nil {
						byPort[uint32(p)] = c.Name
					}
				}
			}
		}
	}
	rows := make([]portRow, 0, len(ports))
	for _, p := range ports {
		rows = append(rows, portRow{metricsPortAlias: p, Container: byPort[p.Port]})
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleMetricsLatest returns the newest sample plus the last five minutes.
func (s *Server) handleMetricsLatest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"latest": s.sampler.Latest(),
		"recent": s.sampler.Recent(),
	})
}

// handleMetricsHistory aggregates stored samples. range: 1h, 6h, 24h, 7d.
func (s *Server) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	var since, step time.Duration
	switch r.URL.Query().Get("range") {
	case "", "1h":
		since, step = time.Hour, 30*time.Second
	case "6h":
		since, step = 6*time.Hour, 2*time.Minute
	case "24h":
		since, step = 24*time.Hour, 5*time.Minute
	case "7d":
		since, step = 7*24*time.Hour, 30*time.Minute
	default:
		writeJSON(w, http.StatusBadRequest, api.Error{Error: "invalid", Message: "range must be 1h, 6h, 24h or 7d"})
		return
	}
	points, err := s.sampler.History(r.Context(), time.Now().Add(-since), step)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: err.Error()})
		return
	}
	if points == nil {
		points = []metricsPointAlias{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stepSeconds": int(step.Seconds()), "points": points})
}

// handleMetricsLive streams samples as server-sent events every two seconds.
func (s *Server) handleMetricsLive(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, api.Error{Error: "internal", Message: "streaming unsupported"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	// no-transform is the part that is not about caching: it tells a CDN or a
	// proxy not to recompress this, and a compressor in front of a stream holds
	// its first kilobyte back — which for a stream is however long the work
	// takes. Traefik's own compressor is told the same thing by content type,
	// in the middleware Islet writes for it.
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(m any) bool {
		b, err := json.Marshal(m)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: sample\ndata: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if latest := s.sampler.Latest(); latest != nil && !send(latest) {
		return
	}
	ch, cancel := s.sampler.Subscribe()
	defer cancel()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			if !send(m) {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
