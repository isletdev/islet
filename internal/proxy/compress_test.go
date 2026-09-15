package proxy

import (
	"strings"
	"testing"
)

// Compression in front of a stream.
//
// Traefik's compressor holds the first kilobyte of a response back to decide
// whether compressing is worth the CPU. For an ordinary page that is invisible.
// For a stream the first kilobyte is however long the work takes — so the
// assistant, container logs, a Compose deploy and a ping all reached the
// browser as nothing at all, and then as a gateway timeout, while curl without
// Accept-Encoding saw them stream perfectly. That difference is why this went
// unnoticed: every test made with curl defaults was a test with compression
// switched off.
func TestStreamsAreNotCompressed(t *testing.T) {
	out, err := Render([]Domain{{
		ID: "d", Host: "panel.example.com", TargetType: "panel",
		TLS: "letsencrypt", Enabled: true, PassHost: true,
	}}, "https://panel:9443")
	if err != nil {
		t.Fatal(err)
	}
	y := string(out)
	if !strings.Contains(y, "islet-compress") {
		t.Fatal("compression is gone entirely; the panel bundle wants it")
	}
	for _, ct := range []string{"text/event-stream", "application/x-ndjson"} {
		if !strings.Contains(y, ct) {
			t.Errorf("%s is not excluded from compression, so every endpoint serving it buffers:\n%s", ct, y)
		}
	}
	// The exclusion has to sit under the compress middleware rather than
	// anywhere else in the document.
	i := strings.Index(y, "islet-compress")
	j := strings.Index(y, "excludedContentTypes")
	if j < i {
		t.Errorf("excludedContentTypes is not part of islet-compress:\n%s", y)
	}
}
