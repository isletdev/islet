// Package metrics samples host resource usage, keeps a short live buffer for
// streaming, persists one sample every 10 seconds, and answers history
// queries from SQLite.
package metrics

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Sample is one reading of the host.
type Sample struct {
	TS        time.Time `json:"ts"`
	CPUPct    float64   `json:"cpuPct"`
	Load1     float64   `json:"load1"`
	Load5     float64   `json:"load5"`
	Load15    float64   `json:"load15"`
	MemUsed   uint64    `json:"memUsed"`
	MemTotal  uint64    `json:"memTotal"`
	SwapUsed  uint64    `json:"swapUsed"`
	SwapTotal uint64    `json:"swapTotal"`
	DiskUsed  uint64    `json:"diskUsed"`
	DiskTotal uint64    `json:"diskTotal"`
	NetRx     uint64    `json:"netRx"` // bytes per second
	NetTx     uint64    `json:"netTx"`
	Ifaces    []Iface   `json:"ifaces,omitempty"` // per interface, busiest first; not stored in history
}

// Iface is one network interface's current throughput.
type Iface struct {
	Name string `json:"name"`
	Rx   uint64 `json:"rx"` // bytes per second
	Tx   uint64 `json:"tx"`
}

// Collector reads the host. It is safe for concurrent use.
type Collector struct {
	mu       sync.Mutex
	rootPath string
	lastNet  *gnet.IOCountersStat
	lastAt   time.Time
	lastPer  map[string]gnet.IOCountersStat
}

// NewCollector prepares a collector. The first CPU and network readings need
// a previous point, so the first Sample after construction reports zero rates.
func NewCollector() *Collector {
	root := "/"
	if runtime.GOOS == "windows" {
		root = os.Getenv("SystemDrive") + `\`
	}
	c := &Collector{rootPath: root}
	_, _ = cpu.Percent(0, false) // prime the delta
	if io, err := gnet.IOCounters(false); err == nil && len(io) > 0 {
		c.lastNet, c.lastAt = &io[0], time.Now()
	}
	return c
}

// Collect takes one sample.
func (c *Collector) Collect(ctx context.Context) Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	prevAt := c.lastAt
	s := Sample{TS: now.UTC().Truncate(time.Second)}

	if pct, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pct) > 0 {
		s.CPUPct = round1(pct[0])
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		s.Load1, s.Load5, s.Load15 = round2(l.Load1), round2(l.Load5), round2(l.Load15)
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		s.MemUsed, s.MemTotal = m.Used, m.Total
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		s.SwapUsed, s.SwapTotal = sw.Used, sw.Total
	}
	if d, err := disk.UsageWithContext(ctx, c.rootPath); err == nil {
		s.DiskUsed, s.DiskTotal = d.Used, d.Total
	}
	if io, err := gnet.IOCountersWithContext(ctx, false); err == nil && len(io) > 0 {
		cur := io[0]
		if c.lastNet != nil {
			dt := now.Sub(c.lastAt).Seconds()
			if dt > 0 {
				s.NetRx = uint64(float64(cur.BytesRecv-c.lastNet.BytesRecv) / dt)
				s.NetTx = uint64(float64(cur.BytesSent-c.lastNet.BytesSent) / dt)
			}
		}
		c.lastNet, c.lastAt = &cur, now
	}
	if per, err := gnet.IOCountersWithContext(ctx, true); err == nil {
		dt := now.Sub(prevAt).Seconds()
		next := make(map[string]gnet.IOCountersStat, len(per))
		for _, p := range per {
			next[p.Name] = p
			if p.Name == "lo" || strings.HasPrefix(p.Name, "veth") || strings.HasPrefix(p.Name, "br-") || p.Name == "docker0" {
				continue
			}
			prev, ok := c.lastPer[p.Name]
			if !ok || dt <= 0 || (p.BytesRecv == 0 && p.BytesSent == 0) {
				continue
			}
			s.Ifaces = append(s.Ifaces, Iface{Name: p.Name, Rx: uint64(float64(p.BytesRecv-prev.BytesRecv) / dt), Tx: uint64(float64(p.BytesSent-prev.BytesSent) / dt)})
		}
		c.lastPer = next
		sort.Slice(s.Ifaces, func(i, j int) bool { return s.Ifaces[i].Rx+s.Ifaces[i].Tx > s.Ifaces[j].Rx+s.Ifaces[j].Tx })
		if len(s.Ifaces) > 6 {
			s.Ifaces = s.Ifaces[:6]
		}
	}
	return s
}

// HostInfo describes the machine.
type HostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platformVersion"`
	Kernel          string `json:"kernel"`
	Arch            string `json:"arch"`
	CPUCount        int    `json:"cpuCount"`
	CPUModel        string `json:"cpuModel"`
	MemTotal        uint64 `json:"memTotal"`
	DiskTotal       uint64 `json:"diskTotal"`
	BootTime        int64  `json:"bootTime"`
	UptimeSeconds   uint64 `json:"uptimeSeconds"`
	Timezone        string `json:"timezone"`
}

// Host returns static-ish facts about the machine.
func (c *Collector) Host(ctx context.Context) HostInfo {
	var h HostInfo
	if hi, err := host.InfoWithContext(ctx); err == nil {
		h.Hostname, h.OS, h.Platform, h.PlatformVersion, h.Kernel = hi.Hostname, hi.OS, hi.Platform, hi.PlatformVersion, hi.KernelVersion
		h.BootTime, h.UptimeSeconds = int64(hi.BootTime), hi.Uptime
	}
	h.Arch = runtime.GOARCH
	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		h.CPUCount = n
	}
	if info, err := cpu.InfoWithContext(ctx); err == nil && len(info) > 0 {
		h.CPUModel = info[0].ModelName
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		h.MemTotal = m.Total
	}
	if d, err := disk.UsageWithContext(ctx, c.rootPath); err == nil {
		h.DiskTotal = d.Total
	}
	if z, _ := time.Now().Zone(); z != "" {
		h.Timezone = z
	}
	return h
}

// Process is one row of the process table.
type Process struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	User    string  `json:"user"`
	CPUPct  float64 `json:"cpuPct"`
	MemRSS  uint64  `json:"memRss"`
	Started int64   `json:"started"`
}

// TopProcesses returns the busiest processes by CPU, then memory.
func (c *Collector) TopProcesses(ctx context.Context, limit int) ([]Process, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(procs))
	for _, p := range procs {
		name, err := p.NameWithContext(ctx)
		if err != nil || name == "" {
			continue
		}
		row := Process{PID: p.Pid, Name: name}
		if v, err := p.CPUPercentWithContext(ctx); err == nil {
			row.CPUPct = round1(v)
		}
		if m, err := p.MemoryInfoWithContext(ctx); err == nil && m != nil {
			row.MemRSS = m.RSS
		}
		if u, err := p.UsernameWithContext(ctx); err == nil {
			row.User = u
		}
		if t, err := p.CreateTimeWithContext(ctx); err == nil {
			row.Started = t / 1000
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CPUPct != out[j].CPUPct {
			return out[i].CPUPct > out[j].CPUPct
		}
		return out[i].MemRSS > out[j].MemRSS
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Port is a listening socket.
type Port struct {
	Proto   string `json:"proto"`
	Address string `json:"address"`
	Port    uint32 `json:"port"`
	PID     int32  `json:"pid"`
	Process string `json:"process"`
}

// ListeningPorts lists TCP listeners and bound UDP sockets.
func (c *Collector) ListeningPorts(ctx context.Context) ([]Port, error) {
	conns, err := gnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil, err
	}
	names := map[int32]string{}
	seen := map[string]bool{}
	var out []Port
	for _, cn := range conns {
		var proto string
		switch {
		case cn.Type == 1 && cn.Status == "LISTEN": // SOCK_STREAM
			proto = "tcp"
		case cn.Type == 2 && cn.Raddr.Port == 0: // SOCK_DGRAM, bound
			proto = "udp"
		default:
			continue
		}
		key := fmt.Sprintf("%s|%s|%d", proto, cn.Laddr.IP, cn.Laddr.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		p := Port{Proto: proto, Address: cn.Laddr.IP, Port: cn.Laddr.Port, PID: cn.Pid}
		if cn.Pid > 0 {
			if n, ok := names[cn.Pid]; ok {
				p.Process = n
			} else if pr, err := process.NewProcessWithContext(ctx, cn.Pid); err == nil {
				if n, err := pr.NameWithContext(ctx); err == nil {
					names[cn.Pid] = n
					p.Process = n
				}
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out, nil
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
