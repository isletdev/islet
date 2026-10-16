package vpn

import (
	"context"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Reachable is something listening on this server that a device in the tunnel
// reaches at the server's tunnel address. Read from the sockets themselves,
// not from what Islet thinks it started: a Postgres somebody ran by hand is
// as reachable as one from the catalog.
type Reachable struct {
	Port    int    `json:"port"`
	Process string `json:"process"`
	// App is the container behind a port Docker publishes, filled in by the
	// caller, which is the one that knows about Docker.
	App string `json:"app,omitempty"`
	// LoopbackOnly is a service bound to 127.0.0.1: nothing outside this
	// machine reaches it except through the tunnel.
	LoopbackOnly bool `json:"loopbackOnly"`
}

var ssUser = regexp.MustCompile(`users:\(\("([^"]+)"`)

// Reach lists the TCP services a device in the tunnel can open, by port.
func (s *Service) Reach(ctx context.Context) []Reachable {
	res, err := s.run.Read(ctx, "ss", "-ltnpH")
	if err != nil {
		return nil
	}
	return parseSS(res.Stdout, s.Config(ctx).Address())
}

func parseSS(out, tunnel string) []Reachable {
	by := map[int]*Reachable{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		ap, err := netip.ParseAddrPort(strings.Replace(f[3], "*:", "0.0.0.0:", 1))
		if err != nil {
			// ss prints a scoped address as [fe80::1]%eth0:port, which is never
			// one a tunnel reaches anyway.
			continue
		}
		a := ap.Addr().Unmap()
		loop := a.IsLoopback()
		if !(loop || a.IsUnspecified() || a.String() == tunnel) {
			continue // bound to some other address: the tunnel cannot reach it
		}
		if loop && a != netip.MustParseAddr("127.0.0.1") {
			continue // 127.0.0.53 and friends: only 127.0.0.1 is forwarded to
		}
		port := int(ap.Port())
		r := by[port]
		if r == nil {
			r = &Reachable{Port: port, LoopbackOnly: true}
			by[port] = r
		}
		if !loop {
			r.LoopbackOnly = false
		}
		if m := ssUser.FindStringSubmatch(line); m != nil && r.Process == "" {
			r.Process = m[1]
		}
	}
	out2 := make([]Reachable, 0, len(by))
	for _, r := range by {
		out2 = append(out2, *r)
	}
	sort.Slice(out2, func(i, j int) bool { return out2[i].Port < out2[j].Port })
	return out2
}

var dockerPort = regexp.MustCompile(`(?:[\d.]+|\[?::\]?):(\d+)->`)

// PublishedPorts reads the host ports out of a container's `docker ps` ports
// column, "127.0.0.1:5433->5432/tcp, 0.0.0.0:80->80/tcp".
func PublishedPorts(ports string) []int {
	var out []int
	for _, m := range dockerPort.FindAllStringSubmatch(ports, -1) {
		if p, err := strconv.Atoi(m[1]); err == nil {
			out = append(out, p)
		}
	}
	return out
}
