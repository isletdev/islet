package tlsutil

import (
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Split serves HTTPS and plain HTTP on one port, telling them apart by the
// first byte a client sends: a TLS handshake always opens with 0x16, and no
// HTTP method starts with it.
//
// Plain HTTP is for the panel inside the VPN. WireGuard already encrypts and
// authenticates every packet, and the panel reached at http://10.66.0.1 has
// no certificate warning to click through — which on a phone, or for a
// browser that will not remember the exception for an address, is the
// difference between using it and not. So plain is answered only when
// plainOK says the client is in the tunnel; anybody else who speaks HTTP to
// the port goes to other, which redirects them to HTTPS.
//
// Accept never waits on a client: each connection is read in a goroutine of
// its own, with a deadline, so one that sends nothing holds up nobody.
func Split(ln net.Listener, cfg *tls.Config, plainOK func(netip.Addr) bool) (panel, other net.Listener) {
	p := &chanListener{ln: ln, c: make(chan net.Conn), done: make(chan struct{})}
	o := &chanListener{ln: ln, c: make(chan net.Conn), done: p.done, once: &p.closeOnce}
	p.once = &p.closeOnce
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				p.close()
				return
			}
			go classify(c, cfg, plainOK, p, o)
		}
	}()
	return p, o
}

func classify(c net.Conn, cfg *tls.Config, plainOK func(netip.Addr) bool, panel, other *chanListener) {
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var b [1]byte
	if _, err := c.Read(b[:]); err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	pc := &peeked{Conn: c, first: b[0], pending: true}
	var out net.Conn = pc
	to := panel
	switch {
	case b[0] == 0x16:
		out = tls.Server(pc, cfg)
	case !plainOK(remoteIP(c)):
		to = other
	}
	select {
	case to.c <- out:
	case <-panel.done:
		_ = c.Close()
	}
}

func remoteIP(c net.Conn) netip.Addr {
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		ip, _ := netip.AddrFromSlice(a.IP)
		return ip.Unmap()
	}
	return netip.Addr{}
}

// peeked gives back the byte classify read.
type peeked struct {
	net.Conn
	first   byte
	pending bool
}

func (p *peeked) Read(b []byte) (int, error) {
	if p.pending && len(b) > 0 {
		p.pending = false
		b[0] = p.first
		return 1, nil
	}
	return p.Conn.Read(b)
}

type chanListener struct {
	ln        net.Listener
	c         chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
	once      *sync.Once
}

func (l *chanListener) close() {
	l.once.Do(func() {
		close(l.done)
	})
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.c:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.close()
	return l.ln.Close()
}

func (l *chanListener) Addr() net.Addr { return l.ln.Addr() }
