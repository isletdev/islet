package media

import (
	"strings"
	"testing"
)

// The formats are a list on purpose rather than an ffmpeg command line somebody
// fills in, so the list is what there is to get wrong.
func TestTheVideoFormatsAreFoundByNameAndNothingElseIs(t *testing.T) {
	for _, want := range []string{"mp4-1080", "mp4-720", "mp4-480"} {
		f, ok := VideoFormatByName(want)
		if !ok {
			t.Fatalf("%s is not offered", want)
		}
		if f.Height <= 0 || f.Label == "" || f.CRF <= 0 {
			t.Errorf("%s is half-described: %+v", want, f)
		}
	}
	if _, ok := VideoFormatByName("mp4-4320"); ok {
		t.Error("a format nobody defined was found")
	}
	// A preset name and a format name share one URL space, so they must not
	// collide: "thumb" is an image preset and must never resolve to a video.
	if _, ok := VideoFormatByName("thumb"); ok {
		t.Error("an image preset name resolves as a video format")
	}
}

// A rendition lives beside the original under the same prefix the image
// variants use, so a bucket has one place where derived bytes are and a
// lifecycle rule has one prefix to match.
func TestARenditionIsKeyedBesideItsOriginal(t *testing.T) {
	f, _ := VideoFormatByName("mp4-720")
	key := f.key("shop/2026/09/holiday.mov")
	if !strings.HasPrefix(key, "shop/2026/09/"+derivativeInfix+"/") {
		t.Errorf("a rendition is not under the derivatives prefix: %s", key)
	}
	if !strings.HasSuffix(key, ".mp4") {
		t.Errorf("a rendition does not end in .mp4: %s", key)
	}
	if !strings.Contains(key, "mp4-720") {
		t.Errorf("two formats of one video could collide: %s", key)
	}
	other, _ := VideoFormatByName("mp4-480")
	if other.key("shop/holiday.mov") == key {
		t.Error("two formats produced the same key")
	}
}

// The command is the whole of what this feature does to a server, so it is
// pinned: a flag lost in an edit is minutes of CPU producing the wrong file.
func TestTheEncodeCommandSaysWhatItMeans(t *testing.T) {
	f, _ := VideoFormatByName("mp4-720")
	args := encodeArgs("src-1", "out-1.mp4", f)
	joined := strings.Join(args, " ")

	if args[0] != "exec" || args[1] != WorkerName {
		t.Errorf("the encode no longer runs in the worker: %v", args[:2])
	}
	if args[len(args)-1] != "out-1.mp4" {
		t.Errorf("the output is not the last argument, which is where ffmpeg looks: %v", args)
	}
	for _, want := range []string{"-nostdin", "-progress pipe:1", "-c:v libx264", "-pix_fmt yuv420p", "-movflags +faststart", "-crf 23"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the command no longer carries %q: %s", want, joined)
		}
	}
	// Never upscale: the filter has to be a minimum against the source height,
	// not the height on its own.
	if !strings.Contains(joined, "scale=-2:min(720") {
		t.Errorf("the scale filter would upscale a smaller source: %s", joined)
	}
	// -y overwrites the staging file from a previous attempt; without it ffmpeg
	// stops to ask, and nothing is there to answer.
	if !strings.Contains(joined, "-y") {
		t.Error("the encode would block on an overwrite prompt")
	}
}

// Progress comes out of ffmpeg's own stream. The unit is microseconds under two
// different names, one of which says milliseconds and is not.
func TestProgressIsReadFromFFmpegsOwnLines(t *testing.T) {
	for _, c := range []struct {
		line string
		want float64
		ok   bool
	}{
		{"out_time_us=12000000", 12, true},
		{"out_time_ms=12000000", 12, true},
		{"out_time_us=0", 0, true},
		{"frame=120", 0, false},
		{"progress=continue", 0, false},
		{"out_time_us=N/A", 0, false},
		{"", 0, false},
	} {
		got, ok := elapsed(c.line)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q gave (%v, %v), want (%v, %v)", c.line, got, ok, c.want, c.ok)
		}
	}
}
