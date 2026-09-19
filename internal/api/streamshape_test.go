package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The panel reads two different stream shapes, and a handler has to pick the
// one its caller reads.
//
// postStream parses server-sent events. postNDJSON parses newline-delimited
// JSON. They are not interchangeable and nothing was checking: the media
// worker's install emitted NDJSON to a page calling postStream, so the parser
// found no events, saw the stream end without its end event, and threw "the
// connection closed before the command finished" — after a build that had
// worked. An install that succeeds and reports failure is worse than one that
// fails, because the next thing anybody does is run it again.
//
// This reads the panel for which paths are fetched with postStream and the
// daemon for which handlers write NDJSON, and fails when one is the other.
func TestAStreamIsTheShapeItsCallerReads(t *testing.T) {
	// Paths the panel fetches expecting SSE. Template literals are reduced to a
	// prefix, because "/api/v1/apps/${app.id}/deploy" only has to match the
	// route it was written for.
	callers := regexp.MustCompile(`postStream\(\s*[` + "`" + `"]([^` + "`" + `"]*)`)
	var sse []string
	for _, f := range panelFiles(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range callers.FindAllStringSubmatch(string(src), -1) {
			p := m[1]
			if !strings.HasPrefix(p, "/api/") {
				continue // the definition in stream.ts itself
			}
			if i := strings.Index(p, "${"); i >= 0 {
				p = p[:i]
			}
			sse = append(sse, p)
		}
	}
	if len(sse) < 5 {
		t.Fatalf("only found %d postStream calls in the panel; the parser has probably stopped matching", len(sse))
	}

	// Handlers that write newline-delimited JSON, and the routes that reach
	// them. Read from the source for the same reason the role table is: a
	// handler added tomorrow is covered without anybody remembering this file.
	ndjson := map[string]bool{}
	for _, f := range apiFiles(t) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range strings.Split(string(src), "\nfunc ") {
			name := regexp.MustCompile(`^\(s \*Server\) (\w+)\(`).FindStringSubmatch(block)
			if name == nil {
				continue
			}
			if strings.Contains(block, `"application/x-ndjson"`) {
				ndjson[name[1]] = true
			}
		}
	}

	routes, err := os.ReadFile(filepath.Join("server.go"))
	if err != nil {
		t.Fatal(err)
	}
	route := regexp.MustCompile(`mux\.HandleFunc\("(\w+) ([^"]+)",(.*)\)`)
	for _, m := range route.FindAllStringSubmatch(string(routes), -1) {
		method, path, wiring := m[1], m[2], m[3]
		if method != "POST" {
			continue
		}
		handler := regexp.MustCompile(`s\.(handle\w+)`).FindStringSubmatch(wiring)
		if handler == nil || !ndjson[handler[1]] {
			continue
		}
		for _, p := range sse {
			if strings.HasPrefix(path, p) {
				t.Errorf("the panel reads %s as server-sent events, but %s writes newline-delimited JSON:\n"+
					"  it will report a successful command as a failed one", p, handler[1])
			}
		}
	}
}

func panelFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	root := filepath.Join("..", "..", "web", "app", "src")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(p, ".tsx") || strings.HasSuffix(p, ".ts")) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Skipf("the panel source is not here: %v", err)
	}
	return out
}

func apiFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			out = append(out, e.Name())
		}
	}
	return out
}
