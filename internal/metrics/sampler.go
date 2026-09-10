package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/isletdev/islet/internal/store"
)

const (
	liveInterval    = 2 * time.Second
	persistEvery    = 5 // ticks, so one row every 10 seconds
	retention       = 7 * 24 * time.Hour
	liveBufferCount = 150 // five minutes at 2 s
)

// Sampler runs the collector on a schedule, fans live samples out to
// subscribers, and persists a subset to the store.
type Sampler struct {
	c   *Collector
	st  *store.Store
	log *slog.Logger

	mu     sync.RWMutex
	latest *Sample
	ring   []Sample
	subs   map[chan Sample]struct{}
}

// NewSampler wires a collector to the store.
func NewSampler(c *Collector, st *store.Store, log *slog.Logger) *Sampler {
	return &Sampler{c: c, st: st, log: log, subs: make(map[chan Sample]struct{})}
}

// Run blocks until ctx is done.
func (s *Sampler) Run(ctx context.Context) {
	tick := time.NewTicker(liveInterval)
	defer tick.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-purge.C:
			s.purge(ctx)
		case <-tick.C:
			sample := s.c.Collect(ctx)
			s.publish(sample)
			n++
			if n%persistEvery == 0 {
				s.persist(ctx, sample)
			}
		}
	}
}

// Latest returns the most recent sample, if any.
func (s *Sampler) Latest() *Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest
}

// Recent returns the in-memory ring (last five minutes at 2 s).
func (s *Sampler) Recent() []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Sample, len(s.ring))
	copy(out, s.ring)
	return out
}

// Subscribe returns a channel of live samples and a cancel function.
func (s *Sampler) Subscribe() (<-chan Sample, func()) {
	ch := make(chan Sample, 8)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

func (s *Sampler) publish(sample Sample) {
	s.mu.Lock()
	cp := sample
	s.latest = &cp
	s.ring = append(s.ring, sample)
	if len(s.ring) > liveBufferCount {
		s.ring = s.ring[len(s.ring)-liveBufferCount:]
	}
	for ch := range s.subs {
		select {
		case ch <- sample:
		default: // slow consumer: drop rather than block the sampler
		}
	}
	s.mu.Unlock()
}

func (s *Sampler) persist(ctx context.Context, m Sample) {
	_, err := s.st.DB.ExecContext(ctx, `INSERT OR REPLACE INTO metrics_samples
		(server_id, ts, cpu_pct, load1, load5, load15, mem_used, mem_total, swap_used, swap_total, disk_used, disk_total, net_rx, net_tx)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.st.ServerID, m.TS.Unix(), m.CPUPct, m.Load1, m.Load5, m.Load15, m.MemUsed, m.MemTotal, m.SwapUsed, m.SwapTotal,
		m.DiskUsed, m.DiskTotal, m.NetRx, m.NetTx)
	if err != nil {
		s.log.Warn("metrics persist failed", "err", err)
	}
}

func (s *Sampler) purge(ctx context.Context) {
	cut := time.Now().Add(-retention).Unix()
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM metrics_samples WHERE server_id = ? AND ts < ?`, s.st.ServerID, cut); err != nil {
		s.log.Warn("metrics purge failed", "err", err)
	}
}

// Point is one aggregated bucket of history.
type Point struct {
	TS        int64   `json:"ts"`
	CPUPct    float64 `json:"cpuPct"`
	Load1     float64 `json:"load1"`
	MemUsed   uint64  `json:"memUsed"`
	MemTotal  uint64  `json:"memTotal"`
	DiskUsed  uint64  `json:"diskUsed"`
	DiskTotal uint64  `json:"diskTotal"`
	NetRx     uint64  `json:"netRx"`
	NetTx     uint64  `json:"netTx"`
}

// History returns samples since `since`, averaged into buckets of `step`.
func (s *Sampler) History(ctx context.Context, since time.Time, step time.Duration) ([]Point, error) {
	stepSec := int64(step.Seconds())
	if stepSec < 10 {
		stepSec = 10
	}
	rows, err := s.st.DB.QueryContext(ctx, `SELECT (ts / ?) * ? AS b, AVG(cpu_pct), AVG(load1), AVG(mem_used), MAX(mem_total),
		AVG(disk_used), MAX(disk_total), AVG(net_rx), AVG(net_tx)
		FROM metrics_samples WHERE server_id = ? AND ts >= ? GROUP BY b ORDER BY b`,
		stepSec, stepSec, s.st.ServerID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		var cpu, l1, mu, du, rx, tx float64
		if err := rows.Scan(&p.TS, &cpu, &l1, &mu, &p.MemTotal, &du, &p.DiskTotal, &rx, &tx); err != nil {
			return nil, err
		}
		p.CPUPct, p.Load1 = round1(cpu), round2(l1)
		p.MemUsed, p.DiskUsed, p.NetRx, p.NetTx = uint64(mu), uint64(du), uint64(rx), uint64(tx)
		out = append(out, p)
	}
	return out, rows.Err()
}
