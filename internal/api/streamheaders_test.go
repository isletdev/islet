package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every response that streams has to say no-transform.
//
// It is not about caching. A compressor between the daemon and the browser —
// Traefik here, a CDN beyond it — holds a response's first kilobyte back to
// decide whether compressing is worth it, and a stream's first kilobyte is
// however long the work takes. The symptom is the worst kind: the endpoint is
// correct, curl without Accept-Encoding shows it streaming perfectly, and the
// browser sees nothing and then a gateway timeout.
//
// So this checks the header is set wherever a streaming content type is, and
// it scans source because there is no single place these go through.
func TestEveryStreamingResponseRefusesToBeTransformed(t *testing.T) {
	setsStream := regexp.MustCompile(`Set\("Content-Type", "(text/event-stream|application/x-ndjson)"\)`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			if !setsStream.MatchString(line) {
				continue
			}
			checked++
			// The Cache-Control for this response is set within a few lines of
			// its content type in every one of these handlers.
			window := strings.Join(lines[max(0, i-6):min(len(lines), i+8)], "\n")
			if !strings.Contains(window, "no-transform") {
				t.Errorf("%s:%d sets a streaming content type without no-transform, so a compressor in front will buffer it:\n%s", e.Name(), i+1, window)
			}
		}
	}
	if checked < 4 {
		t.Fatalf("only found %d streaming responses; the scan is not reading what it thinks it is", checked)
	}
}
