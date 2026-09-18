package media

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Storage is a place to put bytes.
//
// Four methods and one optional fifth. Anything an application can do to an
// object is one of these, which is what makes "this bucket is on R2 now" a
// change of one row rather than a change to the service.
type Storage interface {
	// Put writes an object. size is required: S3 wants a length and a local
	// disk is happy either way, so the interface takes the stricter of the two.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens one for reading.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Stat is size and content type without the bytes.
	Stat(ctx context.Context, key string) (int64, string, error)
	// Delete removes one. Removing what is not there is not an error: it is the
	// state that was asked for.
	Delete(ctx context.Context, key string) error
}

// Presigner is a storage that can hand out a URL somebody else uploads to.
//
// Optional, because local disk cannot: there is no second address for a file on
// this machine. That is the whole reason it is a separate interface rather than
// a method that returns an error on one driver — the caller has to choose a
// different upload path, not handle a failure.
type Presigner interface {
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ErrNoObject is a key that is not there.
var ErrNoObject = errors.New("no such object")

// ---- local ----------------------------------------------------------------

// Local keeps objects on this server's disk, under the data directory.
//
// The cheapest thing that works, and the right default: a server with no cloud
// account should still be able to switch the service on and have it do the job.
type Local struct{ Root string }

func (l *Local) path(key string) (string, error) {
	clean := path.Clean("/" + key)
	if strings.Contains(clean, "..") {
		return "", ErrNoObject
	}
	return filepath.Join(l.Root, filepath.FromSlash(strings.TrimPrefix(clean, "/"))), nil
}

func (l *Local) Put(ctx context.Context, key string, r io.Reader, size int64, _ string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	// Written beside the target and renamed, so a reader never sees half an
	// object and an interrupted upload leaves nothing to clean up.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (l *Local) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, ErrNoObject
	}
	return f, err
}

func (l *Local) Stat(ctx context.Context, key string) (int64, string, error) {
	p, err := l.path(key)
	if err != nil {
		return 0, "", err
	}
	st, err := os.Stat(p)
	if os.IsNotExist(err) {
		return 0, "", ErrNoObject
	}
	if err != nil {
		return 0, "", err
	}
	return st.Size(), "", nil
}

func (l *Local) Delete(ctx context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// LocalPath is where an object is on disk, for the transform worker to read
// without a round trip through this process.
func (l *Local) LocalPath(key string) (string, error) { return l.path(key) }

// ---- helpers --------------------------------------------------------------

// escapePath encodes a key for a URL path without turning its slashes into
// %2F, which is the one thing every hand-written S3 client gets wrong.
func escapePath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
