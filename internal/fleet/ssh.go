package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// How a server is reached during the join, and only during the join.
//
// Whatever the person gives us here is used once, to log in and install the
// controller's own key. It is never written to disk. From then on the
// controller authenticates with a key it generated itself, which can be revoked
// by deleting one line from the server's authorized_keys.
type Credentials struct {
	User       string
	Password   string // used once, discarded
	PrivateKey string // used once, discarded, may be passphrase protected
	Passphrase string
}

// Keypair is the controller's own identity on a managed server.
type Keypair struct {
	PrivatePEM string // stored encrypted
	PublicLine string // what goes into authorized_keys
}

// NewKeypair generates the identity the controller will use from now on.
//
// Ed25519 because it is small, fast and accepted by every current OpenSSH. The
// comment makes the line obvious in authorized_keys, so a person can see what
// it is and remove it.
func NewKeypair(comment string) (*Keypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
	return &Keypair{PrivatePEM: string(pem.EncodeToMemory(block)), PublicLine: line}, nil
}

// Conn is an open SSH connection to a managed server.
type Conn struct {
	client  *ssh.Client
	HostKey string // the key we saw, for pinning
}

// Dial opens a connection.
//
// hostKey is the key we expect. On the first connection it is empty and
// whatever the server presents is recorded and returned, which is
// trust-on-first-use: the same bargain every ssh client makes, and the honest
// one to make from a browser where nobody can read a fingerprint aloud. Every
// connection afterwards must match, so a swapped server is refused.
func Dial(ctx context.Context, addr string, c Credentials, hostKey string) (*Conn, error) {
	var auth []ssh.AuthMethod
	if c.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if c.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKey), []byte(c.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(c.PrivateKey))
		}
		if err != nil {
			if _, ok := err.(*ssh.PassphraseMissingError); ok {
				return nil, errors.New("that key is protected by a passphrase; add it and try again")
			}
			return nil, fmt.Errorf("that private key could not be read: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		auth = append(auth, ssh.Password(c.Password),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				out := make([]string, len(qs))
				for i := range out {
					out[i] = c.Password
				}
				return out, nil
			}))
	}
	if len(auth) == 0 {
		return nil, errors.New("give either a password or a private key")
	}

	seen := ""
	cfg := &ssh.ClientConfig{
		User: c.User,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = string(ssh.MarshalAuthorizedKey(key))
			if hostKey == "" {
				return nil // first contact; the caller records what we saw
			}
			if strings.TrimSpace(seen) != strings.TrimSpace(hostKey) {
				return errors.New("this server's SSH host key has changed since it was added; if that was not expected, someone may be impersonating it")
			}
			return nil
		},
		Timeout: 15 * time.Second,
	}

	d := net.Dialer{Timeout: 15 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, friendlyDialError(addr, err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		_ = raw.Close()
		return nil, friendlySSHError(err)
	}
	return &Conn{client: ssh.NewClient(cc, chans, reqs), HostKey: strings.TrimSpace(seen)}, nil
}

// friendlyDialError says what went wrong at the address, in the words a person
// would use. "dial tcp 203.0.113.9:22: i/o timeout" is accurate and tells
// nobody what to do next.
func friendlyDialError(addr string, err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded"):
		return fmt.Errorf("%s did not answer. Check the address, and that a firewall is not blocking SSH", addr)
	case strings.Contains(s, "refused"):
		return fmt.Errorf("%s refused the connection. Check the SSH port, and that sshd is running", addr)
	case strings.Contains(s, "no such host"):
		return fmt.Errorf("%s could not be looked up. Check the host name", addr)
	case strings.Contains(s, "unreachable"):
		return fmt.Errorf("%s is unreachable from this server", addr)
	default:
		return fmt.Errorf("could not reach %s: %w", addr, err)
	}
}

// friendlySSHError turns the library's wording into something a person can act on.
func friendlySSHError(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "unable to authenticate"):
		return errors.New("the server refused those credentials. Check the user, and that password login is allowed if you gave a password")
	case strings.Contains(s, "host key"):
		return err
	default:
		return fmt.Errorf("ssh: %w", err)
	}
}

func (c *Conn) Close() error { return c.client.Close() }

// Run executes one command and returns its combined output.
func (c *Conn) Run(ctx context.Context, cmd string) (string, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var buf strings.Builder
	sess.Stdout = &buf
	sess.Stderr = &buf
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return buf.String(), ctx.Err()
	}
}

// Stream runs a command and reports each line as it arrives, so the wizard can
// show the install happening rather than a spinner.
func (c *Conn) Stream(ctx context.Context, cmd string, line func(string)) error {
	sess, err := c.client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	pr, pw := io.Pipe()
	sess.Stdout = pw
	sess.Stderr = pw
	go func() {
		defer pr.Close()
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := pr.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				for {
					i := strings.IndexByte(string(buf), '\n')
					if i < 0 {
						break
					}
					line(strings.TrimRight(string(buf[:i]), "\r"))
					buf = buf[i+1:]
				}
			}
			if err != nil {
				if len(buf) > 0 {
					line(string(buf))
				}
				return
			}
		}
	}()

	done := make(chan error, 1)
	go func() { err := sess.Run(cmd); _ = pw.Close(); done <- err }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = pw.Close()
		return ctx.Err()
	}
}

// Tunnel opens a connection to an address on the far side, which is how the
// controller reaches a managed daemon.
//
// The managed panel therefore needs no port open to the internet at all: it
// listens on loopback and this reaches it through the same SSH connection that
// installed it.
func (c *Conn) Tunnel(ctx context.Context, addr string) (net.Conn, error) {
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		conn, err := c.client.Dial("tcp", addr)
		ch <- res{conn, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
