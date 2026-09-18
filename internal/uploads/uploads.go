// Package uploads keeps the files somebody hands the assistant.
//
// The assistant can already do most of what a person can do on this server, but
// everything it worked from had to be already on the machine. "Build me a site"
// with the logo, the brand fonts and the copy sitting on a laptop meant finding
// somewhere to put them first — so the thing a person most wants to start from
// was the one thing they could not give it.
//
// A file arrives here, is checked for malware, and from then on it is an
// absolute path on this server. That is deliberately the whole interface: a
// path is what every tool the assistant has already takes, and what Claude Code
// reads directly, so nothing else in the system has to learn what an attachment
// is.
package uploads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/isletdev/islet/internal/cmdrun"
)

// MaxSize is the most one file may be. Large enough for a phone's video of a
// shopfront, which is a real thing to hand a site builder, and small enough
// that filling the disk takes intent rather than one drop of a folder.
const MaxSize = 256 << 20

// File is one uploaded thing.
type File struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Path is the point of the exercise: where this is on the server, for the
	// model to pass to any tool that takes a path.
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Type    string `json:"type"`
	AddedAt string `json:"addedAt"`
	// Scanner is what checked it, or empty when nothing did. Said out loud
	// rather than implied: "no virus was found" and "nothing looked" are
	// different answers and only one of them is reassuring.
	Scanner string `json:"scanner,omitempty"`
}

// ErrTooBig and friends are the refusals a caller turns into a status.
var (
	ErrTooBig   = fmt.Errorf("a file may be at most %d MB", MaxSize>>20)
	ErrNoName   = errors.New("that upload has no file name")
	ErrNotFound = errors.New("no such upload")
)

// Infected is a file that a scanner objected to. It carries what the scanner
// said, because "rejected" without the signature name is not something anyone
// can act on.
type Infected struct{ Signature string }

func (e *Infected) Error() string {
	if e.Signature == "" {
		return "the virus scanner rejected this file"
	}
	return "the virus scanner rejected this file: " + e.Signature
}

// Service stores uploads under the data directory.
type Service struct {
	dir  string
	cmds *cmdrun.Runner
	log  *slog.Logger
	// scanTimeout bounds one scan. A zip of a brand kit is a few thousand
	// files and clamscan is not fast; a video is one file and large.
	scanTimeout time.Duration
	// scanFile is the check itself, so a test can drive the infected and
	// broken-scanner paths on a machine that has no scanner installed —
	// which is most machines, including the one this is developed on.
	scanFile func(ctx context.Context, actor, path string) (string, error)
}

// New builds the service. The directory is made on first use, not here: a
// daemon nobody uploads to should not grow directories.
func New(cmds *cmdrun.Runner, dataDir string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	s := &Service{dir: filepath.Join(dataDir, "uploads"), cmds: cmds, log: log, scanTimeout: 3 * time.Minute}
	s.scanFile = s.clamav
	return s
}

// Dir is where uploads live, which the assistant is told so it can look for
// itself rather than being handed one path at a time.
func (s *Service) Dir() string { return s.dir }

// Save writes one upload, scans it, and returns where it landed.
//
// The scan happens before the file is anything anyone can reach: it goes into
// a directory of its own, is checked there, and the whole directory is removed
// if the scanner objects — so a rejected file is never listed, never served and
// never has a path to hand to a model.
//
// A directory per upload rather than a name per upload, because the name is
// kept exactly as it was sent. An agent told to use "logo.svg" can say so; one
// told to use "a3f2c1d0-logo.svg" spends the conversation explaining it.
func (s *Service) Save(ctx context.Context, actor, name string, r io.Reader) (*File, error) {
	name = SafeName(name)
	if name == "" {
		return nil, ErrNoName
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, name)
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	// LimitReader with one byte of headroom, so going over the cap is
	// detectable rather than a silently truncated file.
	n, err := io.Copy(f, io.LimitReader(r, MaxSize+1))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && n > MaxSize {
		err = ErrTooBig
	}
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}

	scanner, err := s.scanFile(ctx, actor, dst)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}

	st, err := os.Stat(dst)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &File{
		ID: id, Name: name, Path: dst, Size: st.Size(),
		Type:    kindOf(dst, name),
		AddedAt: st.ModTime().UTC().Format(time.RFC3339),
		Scanner: scanner,
	}, nil
}

// List is everything uploaded, newest first.
//
// Read from the directory rather than from a table. The files are the state;
// a row beside them would be a second answer to "what is here" that drifts the
// first time somebody deletes one with rm.
func (s *Service) List() ([]File, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []File{}, nil
		}
		return nil, err
	}
	out := []File{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt > out[j].AddedAt })
	return out, nil
}

// Get is one upload by id.
func (s *Service) Get(id string) (*File, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	dir := filepath.Join(s.dir, id)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return nil, ErrNotFound
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(dir, e.Name())
		return &File{
			ID: id, Name: e.Name(), Path: p, Size: st.Size(),
			Type:    kindOf(p, e.Name()),
			AddedAt: st.ModTime().UTC().Format(time.RFC3339),
		}, nil
	}
	return nil, ErrNotFound
}

// Remove deletes one upload and the directory holding it.
func (s *Service) Remove(id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	if _, err := s.Get(id); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.dir, id))
}

// ---- scanning -------------------------------------------------------------

// Scanner is the malware scanner this machine has, or empty.
//
// clamdscan first: it asks a running daemon that already holds the signatures,
// which is the difference between a second and half a minute. clamscan loads
// them itself every time, which is slow but works on a machine where nobody
// wanted a resident daemon on a gigabyte of RAM.
func (s *Service) Scanner(ctx context.Context) string {
	if s.cmds == nil {
		return ""
	}
	for _, name := range []string{"clamdscan", "clamscan"} {
		if _, err := s.cmds.Read(ctx, "sh", "-c", "command -v "+name); err == nil {
			return name
		}
	}
	return ""
}

// clamav runs the scanner over one file. An empty name back means nothing was
// installed to look, which is a fact for the caller to pass on rather than a
// failure.
func (s *Service) clamav(ctx context.Context, actor, path string) (string, error) {
	name := s.Scanner(ctx)
	if name == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.scanTimeout)
	defer cancel()
	args := []string{"--no-summary", path}
	if name == "clamdscan" {
		// --fdpass hands the open descriptor over, so the daemon reads a file
		// it would otherwise have no permission to open: uploads live under
		// the data directory, which is the daemon's and not clamav's.
		args = append([]string{"--no-summary", "--fdpass"}, path)
	}
	res, err := s.cmds.Run(ctx, actor, name, args...)
	switch {
	case err == nil:
		return name, nil
	case res.ExitCode == 1:
		return "", &Infected{Signature: signature(res.Stdout)}
	default:
		// The scanner is installed and could not answer. Refusing is the only
		// honest option: the file is unknown, and saying "clean" because the
		// check broke is how a scanner becomes decoration.
		out := strings.TrimSpace(res.Stderr)
		if out == "" {
			out = strings.TrimSpace(res.Stdout)
		}
		if out == "" {
			out = err.Error()
		}
		s.log.Warn("the virus scanner could not check an upload", "scanner", name, "err", out)
		return "", fmt.Errorf("%s could not check this file: %s", name, firstLine(out))
	}
}

// signature pulls the name out of "…/file: Eicar-Signature FOUND".
func signature(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.LastIndex(line, ": "); i >= 0 && strings.HasSuffix(strings.TrimSpace(line), "FOUND") {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[i+2:]), "FOUND"))
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ---- names ----------------------------------------------------------------

// SafeName reduces whatever the browser sent to a plain file name.
//
// Only the base is kept, so "../../etc/passwd" is "passwd", and control
// characters and separators go: a name is displayed in the panel, written to
// disk, and handed to a model that will put it in a shell command.
func SafeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return -1
		case r == '/' || r == 0:
			return -1
		case unicode.IsSpace(r):
			return ' '
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	// Long enough for anything a person named on purpose, short enough to stay
	// inside a file-name limit once a suffix is on it.
	if len(name) > 128 {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		name = name[:128-len(ext)] + ext
	}
	return name
}

// kindOf is the media type, from the extension where there is one and from the
// bytes where there is not — the browser's own claim is not used, because it is
// whatever the uploading page chose to say.
func kindOf(path, name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return strings.TrimSpace(strings.SplitN(t, ";", 2)[0])
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	var head [512]byte
	n, _ := io.ReadFull(f, head[:])
	return strings.TrimSpace(strings.SplitN(http.DetectContentType(head[:n]), ";", 2)[0])
}

func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validID(id string) bool {
	if len(id) != 12 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
