package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Turning a video into something a browser will actually play.
//
// Everything else in this package answers inside the request that asked for it:
// a thumbnail is a second of vips and the second request is a read. A video is
// not that. A three-minute clip is minutes of one core, which is why this is the
// only thing here that is queued rather than done — see internal/work — and why
// nothing transcodes on demand. A caller that asks for a rendition which does
// not exist is told it is not ready, rather than being held open while a 1 vCPU
// box works through it.
//
// The formats are a fixed list rather than an ffmpeg command line somebody fills
// in. A command line would be the most flexible thing here and also a shell: the
// worker runs as root in a container with the bucket's staging directory mounted,
// and "you may pass your own arguments to ffmpeg" is "you may read and write
// files as root". Three heights of H.264 cover what a site needs; a fourth is a
// line in this file when somebody wants one.

// VideoFormat is one rendition a video can be turned into.
type VideoFormat struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Height in pixels. The width follows the source's aspect ratio, and a
	// source shorter than this is left at its own height rather than blown up:
	// upscaling costs the same as downscaling and looks worse than the original.
	Height int `json:"height"`
	// CRF is x264's quality dial, lower being better. 23 is its own default and
	// is about right for the web; 1080 gets 24 because the extra pixels hide it.
	CRF int `json:"-"`
}

// VideoFormats is every rendition this server offers.
//
// H.264 in MP4 with AAC audio, because it is the one combination that plays on
// everything without a fallback. VP9 and AV1 are smaller at the same quality and
// are not here on purpose: on the single core these servers have, a VP9 encode
// of a few minutes of video is an hour or more of full load. That is a decision
// to revisit when there is hardware encoding to hand, not a gap.
var VideoFormats = []VideoFormat{
	{Name: "mp4-1080", Label: "MP4 1080p", Height: 1080, CRF: 24},
	{Name: "mp4-720", Label: "MP4 720p", Height: 720, CRF: 23},
	{Name: "mp4-480", Label: "MP4 480p", Height: 480, CRF: 23},
}

// VideoFormatByName finds one, and says so rather than guessing.
func VideoFormatByName(name string) (VideoFormat, bool) {
	for _, f := range VideoFormats {
		if f.Name == strings.TrimSpace(name) {
			return f, true
		}
	}
	return VideoFormat{}, false
}

func (f VideoFormat) contentType() string { return "video/mp4" }

// key is where a rendition lives: beside the original, under the same
// derivatives prefix the image variants use, named for the format. As with
// those, there is no table of renditions — the name carries everything that
// decides the bytes, so "does this exist" is a question for the bucket.
func (f VideoFormat) key(objectKey string) string {
	return fmt.Sprintf("%s/%s/video-%s.mp4", path.Dir(objectKey), derivativeInfix, f.Name)
}

// TranscodeArgs is what a queued transcode carries.
type TranscodeArgs struct {
	ObjectID string `json:"objectId"`
	Format   string `json:"format"`
}

// Rendition is one format and whether it is there yet.
type VideoRendition struct {
	Format VideoFormat `json:"format"`
	Ready  bool        `json:"ready"`
	Size   int64       `json:"size,omitempty"`
}

// VideoRenditions is what exists for an object, for the panel to draw and for an
// application to ask before it points a <video> at anything.
func (s *Service) VideoRenditions(ctx context.Context, o *Object) ([]VideoRendition, error) {
	b, err := s.Bucket(ctx, o.BucketID)
	if err != nil {
		return nil, err
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return nil, err
	}
	out := make([]VideoRendition, 0, len(VideoFormats))
	for _, f := range VideoFormats {
		r := VideoRendition{Format: f}
		if size, _, err := st.Stat(ctx, f.key(o.Key)); err == nil {
			r.Ready, r.Size = true, size
		}
		out = append(out, r)
	}
	return out, nil
}

// OpenVideo serves a rendition that exists, and says plainly when one does not.
//
// Deliberately not "make it now": that is the difference between this and every
// other derivative here, and hiding it behind a request that takes four minutes
// would be worse than the 409.
func (s *Service) OpenVideo(ctx context.Context, o *Object, formatName string) (*Rendition, error) {
	f, ok := VideoFormatByName(formatName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoPreset, formatName)
	}
	b, err := s.Bucket(ctx, o.BucketID)
	if err != nil {
		return nil, err
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return nil, err
	}
	key := f.key(o.Key)
	size, _, err := st.Stat(ctx, key)
	if err != nil {
		return nil, ErrNotTranscoded
	}
	if o.Visibility == "public" && b.PublicBase != "" {
		return &Rendition{Redirect: strings.TrimSuffix(b.PublicBase, "/") + "/" + key, Immutable: true}, nil
	}
	if pre, ok := st.(Presigner); ok && b.Driver == "s3" {
		if url, err := pre.PresignGet(ctx, key, 10*time.Minute); err == nil {
			return &Rendition{Redirect: url, Immutable: o.Visibility == "public"}, nil
		}
	}
	rc, err := st.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return &Rendition{Body: rc, ContentType: f.contentType(), Size: size, Immutable: true}, nil
}

// ErrNotTranscoded is a rendition that has not been made. It is a separate error
// because the answer to it is "ask for one", not "this is broken".
var ErrNotTranscoded = errors.New("this video has not been transcoded to that format yet")

// Transcode is the work the queue runs. Progress is reported as a fraction of
// the video's own duration, which is the only number here that means anything to
// the person watching it.
func (s *Service) Transcode(ctx context.Context, actor string, a TranscodeArgs, report func(float64, string)) error {
	f, ok := VideoFormatByName(a.Format)
	if !ok {
		return fmt.Errorf("no video format called %q", a.Format)
	}
	o, err := s.Object(ctx, a.ObjectID)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(o.ContentType, "video/") {
		return fmt.Errorf("%s is not a video", o.Filename)
	}
	if t := s.WorkerStatus(ctx); !t.Running {
		if t.Foreign {
			return errors.New("the converter container on this host belongs to another Islet daemon, so nothing here can reach it")
		}
		return errors.New("the converters are not installed on this server")
	}
	b, err := s.Bucket(ctx, o.BucketID)
	if err != nil {
		return err
	}
	st, err := s.storage(ctx, b)
	if err != nil {
		return err
	}

	dstKey := f.key(o.Key)
	if _, _, err := st.Stat(ctx, dstKey); err == nil {
		// Somebody asked twice, or it was made while this waited its turn.
		return nil
	}

	report(0, "fetching the original")
	srcFile, srcRel, err := s.tmpFile("src")
	if err != nil {
		return err
	}
	srcPath := filepath.Join(s.workDir(), srcRel)
	defer func() { _ = os.Remove(srcPath) }()
	rc, err := st.Get(ctx, o.Key)
	if err != nil {
		_ = srcFile.Close()
		return err
	}
	_, err = io.Copy(srcFile, rc)
	_ = rc.Close()
	_ = srcFile.Close()
	if err != nil {
		return err
	}

	seconds := s.probe(ctx, srcRel, o.ContentType).Duration
	dstRel := srcRel + "-" + f.Name + ".mp4"
	dstPath := filepath.Join(s.workDir(), dstRel)
	defer func() { _ = os.Remove(dstPath) }()

	report(0, "encoding")
	if err := s.encode(ctx, actor, srcRel, dstRel, o.Filename, f, seconds, report); err != nil {
		return err
	}

	report(0.97, "storing")
	out, err := os.Open(dstPath)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	info, err := out.Stat()
	if err != nil {
		return err
	}
	if err := st.Put(ctx, dstKey, out, info.Size(), f.contentType()); err != nil {
		return err
	}
	report(1, "")
	return nil
}

// encode runs the one command, and reads how far along it is from ffmpeg itself.
//
// `-progress pipe:1` makes ffmpeg write machine-readable key=value lines as it
// goes, which is why this streams rather than using the ordinary run helper:
// a four-minute encode that reports nothing until it ends is indistinguishable
// from a hung one. It still goes through cmdrun, so the command is in the
// transparency drawer with everything else.
func (s *Service) encode(ctx context.Context, actor, srcRel, dstRel, name string, f VideoFormat, seconds float64, report func(float64, string)) error {
	out, wait, err := s.cmds.Stream(ctx, actor, "docker", encodeArgs(srcRel, dstRel, f)...)
	if err != nil {
		return err
	}

	// Cancelling this context kills `docker exec` on this side, and that is the
	// client: Docker does not pass its death on to the process inside the
	// container. Without this, pressing Cancel marked the task cancelled, freed
	// the queue to start the next encode, and left the first one running to the
	// end — two ffmpegs on a one-core box, which is the exact thing the queue
	// exists to prevent. Found by a release check that watched `docker top`
	// rather than the row.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			s.stopEncode(context.WithoutCancel(ctx), dstRel)
		case <-done:
		}
	}()

	var tail []string
	sc := bufio.NewScanner(out)
	// ffmpeg can write a long line, and the default 64 KB cap ends the scan
	// early — which reads exactly like the encode failing.
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if at, ok := elapsed(line); ok {
			switch {
			case seconds > 0:
				// Capped below 1: storing it still has to happen, and a bar
				// that sits at 100% while something is plainly still going is
				// how people learn not to trust the bar.
				report(min(0.95, at/seconds), "encoding")
			default:
				// A video whose duration nothing could read has no fraction to
				// show. Saying how much of it has been encoded is still a thing
				// that moves, which is most of what a progress bar is for.
				report(0, "encoding "+clock(at))
			}
			continue
		}
		if strings.Contains(line, "=") {
			continue // the rest of ffmpeg's progress block
		}
		if line != "" {
			tail = append(tail, line)
			if len(tail) > 5 {
				tail = tail[1:]
			}
		}
	}
	_ = out.Close()
	werr := wait()

	// Whether it ended or was stopped, this task is not over while a core is
	// still busy with it.
	s.awaitEncodeStopped(context.WithoutCancel(ctx), dstRel)

	if werr != nil {
		if msg := strings.TrimSpace(strings.Join(tail, "; ")); msg != "" {
			return fmt.Errorf("%s", readable(msg, srcRel, dstRel, name))
		}
		return werr
	}
	return nil
}

// readable puts the object's name where ffmpeg put a staging file's, because
// `src-2172116080: Invalid data found` names something the person reading it
// cannot look at and did not choose.
func readable(msg, srcRel, dstRel, name string) string {
	msg = strings.ReplaceAll(msg, dstRel, name)
	return strings.ReplaceAll(msg, srcRel, name)
}

// clock is seconds as minutes and seconds, for a line somebody is reading.
func clock(seconds float64) string {
	total := int(seconds)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// stopEncode kills the encoder itself, inside the container.
//
// `docker top` reports host process ids, which is what makes this possible
// without adding a process tool to the worker image: the encoder is an ordinary
// process on this machine, started by this daemon, and the output file's name is
// unique to this task.
func (s *Service) stopEncode(ctx context.Context, marker string) {
	for _, pid := range s.encodePIDs(ctx, marker) {
		_, _ = s.cmds.Run(ctx, "system", "kill", "-TERM", pid)
	}
}

// awaitEncodeStopped waits for the core to actually be free, escalating once.
//
// Bounded, because the alternative to giving up is a queue that never runs
// anything again; a task that leaves something behind is better reported late
// than never.
func (s *Service) awaitEncodeStopped(ctx context.Context, marker string) {
	deadline := time.Now().Add(20 * time.Second)
	escalated := false
	for time.Now().Before(deadline) {
		pids := s.encodePIDs(ctx, marker)
		if len(pids) == 0 {
			return
		}
		if !escalated && time.Now().After(deadline.Add(-15*time.Second)) {
			for _, pid := range pids {
				_, _ = s.cmds.Run(ctx, "system", "kill", "-KILL", pid)
			}
			escalated = true
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.log.Warn("media: an encode is still running after being told to stop", "output", marker)
}

// encodePIDs is the host pids of anything in the worker still working on this
// task's output.
func (s *Service) encodePIDs(ctx context.Context, marker string) []string {
	res, err := s.cmds.Read(ctx, "docker", "top", WorkerName, "-eo", "pid,args")
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "PID" {
			continue
		}
		out = append(out, fields[0])
	}
	return out
}

// encodeArgs is the whole command, in one place so it can be read and tested
// without a container to run it in.
func encodeArgs(srcRel, dstRel string, f VideoFormat) []string {
	return []string{
		"exec", WorkerName, "ffmpeg", "-nostdin", "-y", "-loglevel", "error", "-progress", "pipe:1",
		"-i", srcRel,
		// Never upscale. A 480p source asked for at 1080 stays 480p, because the
		// alternative is the same picture in a file three times the size.
		"-vf", fmt.Sprintf("scale=-2:min(%d\\,ih)", f.Height),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", strconv.Itoa(f.CRF), "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k",
		// Without this the index is at the end of the file, and a browser has to
		// fetch the whole thing before it can play a second of it.
		"-movflags", "+faststart",
		dstRel,
	}
}

// elapsed reads how far into the video ffmpeg has got, in seconds, from one
// line of its progress stream.
//
// It writes `out_time_us` (and, in older builds, `out_time_ms` — which is also
// microseconds, a name it has never lived down). Both are read here because the
// server's ffmpeg is whatever Debian ships on the day the worker was built.
func elapsed(line string) (float64, bool) {
	for _, key := range []string{"out_time_us=", "out_time_ms="} {
		v, ok := strings.CutPrefix(line, key)
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || n < 0 {
			return 0, false
		}
		return n / 1e6, true
	}
	return 0, false
}
