package media

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The converters.
//
// libvips, ffmpeg and poppler are about three hundred megabytes between them and
// several want cgo. isletd is a single static binary that has to run on a 1 vCPU,
// 1 GB server, and image resizing is not what that budget is for — so the tools
// live in a container Islet owns the lifecycle of, the way it already owns
// Traefik's. A server that never turns media on never pulls the image.
//
// There is no worker protocol and no second binary. The container sits there
// doing nothing and each conversion is a `docker exec` into it with a shared
// directory, which means every conversion goes through cmdrun like every other
// command the daemon runs: visible in the transparency drawer, recorded with its
// exit code and how long it took. An HTTP worker would have been a second API to
// design, version and secure, to save a process spawn of about fifty
// milliseconds on a path that is cached after the first request.

// WorkerImage is the tag built on this server. Built rather than pulled: there
// is no official Islet image to trust yet, the Dockerfile is four lines, and a
// build from Debian's own packages is a supply chain the operator already has.
const WorkerImage = "islet-media:1"

// WorkerName is the container. One per machine, like the proxy.
const WorkerName = "islet-media"

// dockerfile is what the worker is. Deliberately nothing but the tools:
// no server, no port, no code of ours inside it.
const dockerfile = `FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      libvips-tools ffmpeg poppler-utils ca-certificates \
 && rm -rf /var/lib/apt/lists/*
# It is given work with docker exec and has nothing to do on its own.
ENTRYPOINT ["sleep", "infinity"]
`

// Tools is what the worker can do, as the panel reports it.
type Tools struct {
	Running bool   `json:"running"`
	Image   string `json:"image"`
	Vips    string `json:"vips,omitempty"`
	FFmpeg  string `json:"ffmpeg,omitempty"`
	Poppler string `json:"poppler,omitempty"`
}

// workDir is the directory shared with the worker. Everything a conversion
// touches is in here, so the container needs no other access to this machine.
func (s *Service) workDir() string { return filepath.Join(s.dir, "work") }

// WorkerStatus says whether the converters are there and which versions.
func (s *Service) WorkerStatus(ctx context.Context) Tools {
	t := Tools{Image: WorkerImage}
	out, err := s.cmds.Read(ctx, "docker", "inspect", "-f", "{{.State.Running}}", WorkerName)
	if err != nil || strings.TrimSpace(out.Stdout) != "true" {
		return t
	}
	t.Running = true
	t.Vips = firstLine(s.exec(ctx, "vips", "--version"))
	t.FFmpeg = firstWord(firstLine(s.exec(ctx, "ffmpeg", "-version")), 3)
	t.Poppler = firstLine(s.exec(ctx, "pdftoppm", "-v"))
	return t
}

// InstallWorker builds the image and starts the container. Streaming output,
// because a build on a small server takes minutes and a spinner is not an
// answer to "is it working".
func (s *Service) InstallWorker(ctx context.Context, actor string, line func(string)) error {
	if line == nil {
		line = func(string) {}
	}
	dir := filepath.Join(s.dir, "worker")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o640); err != nil {
		return err
	}
	if err := os.MkdirAll(s.workDir(), 0o750); err != nil {
		return err
	}
	line("building " + WorkerImage + " — this takes a few minutes the first time")
	// Streamed rather than run and reported: a build on a small server is
	// several minutes of apt, and a progress box that says nothing for that
	// long is indistinguishable from one that has hung.
	rc, wait, err := s.cmds.Stream(ctx, actor, "docker", "build", "-t", WorkerImage, dir)
	if err != nil {
		return fmt.Errorf("building the converter image failed: %w", err)
	}
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line(sc.Text())
	}
	rc.Close()
	if err := wait(); err != nil {
		return fmt.Errorf("building the converter image failed: %w", err)
	}
	line("image built")
	// Replace any previous container: the image may have changed under it.
	_, _ = s.cmds.Run(ctx, actor, "docker", "rm", "-f", WorkerName)
	args := []string{
		"run", "-d", "--name", WorkerName,
		"--restart", "unless-stopped",
		// It converts files. It has no reason to reach the network, and a
		// converter parsing hostile input is exactly the thing you do not want
		// to be able to call home.
		"--network", "none",
		// Bounded, because a malformed image can make a decoder ask for
		// everything, and this daemon is often the only other thing on the box.
		"--memory", "768m", "--cpus", "1",
		"--security-opt", "no-new-privileges",
		"-v", s.workDir() + ":/work",
		"-w", "/work",
		WorkerImage,
	}
	if _, err := s.cmds.Run(ctx, actor, "docker", args...); err != nil {
		return fmt.Errorf("starting the converter failed: %w", err)
	}
	line("converter running")
	return nil
}

// RemoveWorker stops and deletes the container, leaving the image.
func (s *Service) RemoveWorker(ctx context.Context, actor string) error {
	_, err := s.cmds.Run(ctx, actor, "docker", "rm", "-f", WorkerName)
	return err
}

// exec runs one command inside the worker and returns its output, or "".
func (s *Service) exec(ctx context.Context, name string, args ...string) string {
	full := append([]string{"exec", WorkerName, name}, args...)
	res, err := s.cmds.Read(ctx, "docker", full...)
	if err != nil {
		return ""
	}
	if out := strings.TrimSpace(res.Stdout); out != "" {
		return out
	}
	return strings.TrimSpace(res.Stderr)
}

// run is exec that reports failure, for the conversions themselves.
func (s *Service) run(ctx context.Context, actor string, args ...string) error {
	full := append([]string{"exec", WorkerName}, args...)
	res, err := s.cmds.Run(ctx, actor, "docker", full...)
	if err != nil {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", firstLine(msg))
	}
	return nil
}

// ---- what the converters actually do --------------------------------------

// Probe is what could be read about a file without converting it.
type Probe struct {
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Pages    int     `json:"pages,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// probe reads dimensions, page count or duration, depending on what the file
// is. A failure is not an error worth failing an upload over: an object whose
// size nobody could read is still an object.
func (s *Service) probe(ctx context.Context, rel, contentType string) Probe {
	var p Probe
	switch {
	case strings.HasPrefix(contentType, "image/"):
		w := s.exec(ctx, "vipsheader", "-f", "width", rel)
		h := s.exec(ctx, "vipsheader", "-f", "height", rel)
		p.Width, _ = strconv.Atoi(strings.TrimSpace(w))
		p.Height, _ = strconv.Atoi(strings.TrimSpace(h))
	case contentType == "application/pdf":
		for _, line := range strings.Split(s.exec(ctx, "pdfinfo", rel), "\n") {
			if strings.HasPrefix(line, "Pages:") {
				p.Pages, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
			}
		}
	case strings.HasPrefix(contentType, "video/"), strings.HasPrefix(contentType, "audio/"):
		out := s.exec(ctx, "ffprobe", "-v", "quiet", "-print_format", "json", "-show_streams", "-show_format", rel)
		var probed struct {
			Format  struct{ Duration string } `json:"format"`
			Streams []struct {
				Width, Height int
			} `json:"streams"`
		}
		if json.Unmarshal([]byte(out), &probed) == nil {
			p.Duration, _ = strconv.ParseFloat(probed.Format.Duration, 64)
			for _, st := range probed.Streams {
				if st.Width > 0 {
					p.Width, p.Height = st.Width, st.Height
					break
				}
			}
		}
	}
	return p
}

// derive produces one variant of a source file, both paths relative to the
// shared directory. dst carries the output extension already, because that is
// what tells vips which encoder to use — the options in brackets after it only
// tune the one the name chose.
//
// Which tool depends on the source: an image goes through vips, a PDF is
// rasterised by poppler and then treated as an image, a video gives up a frame
// to ffmpeg and the frame is treated as an image. That is why a preset works on
// all three — the caller asks for "thumb" and does not have to know.
func (s *Service) derive(ctx context.Context, actor, src, dst, contentType string, p Preset) error {
	stage := src
	switch {
	case contentType == "application/pdf":
		// -singlefile makes poppler write exactly dst-without-extension.png.
		page := dst + ".page"
		if err := s.run(ctx, actor, "pdftoppm", "-png", "-r", "72", "-f", "1", "-l", "1", "-singlefile", src, page); err != nil {
			return err
		}
		stage = page + ".png"
		defer func() { _ = os.Remove(filepath.Join(s.workDir(), stage)) }()
	case strings.HasPrefix(contentType, "video/"):
		frame := dst + ".frame.png"
		// One frame, three seconds in, because the first frame of a video is
		// very often black.
		if err := s.run(ctx, actor, "ffmpeg", "-y", "-loglevel", "error", "-ss", "3", "-i", src, "-frames:v", "1", frame); err != nil {
			// A clip shorter than three seconds has no frame there; take the
			// first one instead rather than reporting no thumbnail at all.
			if err := s.run(ctx, actor, "ffmpeg", "-y", "-loglevel", "error", "-i", src, "-frames:v", "1", frame); err != nil {
				return err
			}
		}
		stage = frame
		defer func() { _ = os.Remove(filepath.Join(s.workDir(), stage)) }()
	case !strings.HasPrefix(contentType, "image/"):
		return fmt.Errorf("there is nothing to resize in a %s", contentType)
	}
	args := append([]string{"vips", "thumbnail", stage, dst + vipsOptions(p), strconv.Itoa(p.width())}, vipsArgs(p)...)
	return s.run(ctx, actor, args...)
}

// vipsOptions tunes the encoder that the destination's extension chose.
func vipsOptions(p Preset) string {
	q := p.Quality
	if q <= 0 || q > 100 {
		q = 80
	}
	switch p.Format {
	case "png":
		return "[compression=9,strip]"
	case "avif":
		return fmt.Sprintf("[Q=%d,strip]", q)
	case "jpeg", "jpg":
		return fmt.Sprintf("[Q=%d,strip,optimize_coding]", q)
	default: // webp, and "auto", which is webp
		return fmt.Sprintf("[Q=%d,strip]", q)
	}
}

func vipsArgs(p Preset) []string {
	args := []string{}
	if p.Height > 0 {
		args = append(args, "--height", strconv.Itoa(p.Height))
	}
	if p.Fit == "contain" {
		args = append(args, "--size", "down")
	} else {
		// cover crops to fill the box, which is what a square avatar out of a
		// landscape photograph means.
		args = append(args, "--crop", "centre", "--size", "down")
	}
	return args
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func firstWord(s string, n int) string {
	f := strings.Fields(s)
	if len(f) >= n {
		return f[n-1]
	}
	return s
}
