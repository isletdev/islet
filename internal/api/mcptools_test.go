package api

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/auth"
	"github.com/isletdev/islet/internal/proxy"
)

func routes(t *testing.T) []route {
	t.Helper()
	s := &Server{}
	// curatedTools builds closures over s; the table itself is what is tested,
	// so it is rebuilt here the same way.
	var got []route
	for _, tool := range s.curatedTools() {
		got = append(got, route{name: tool.Name, desc: tool.Description, scope: tool.Scope, method: tool.Method, path: tool.Path})
	}
	return got
}

// A tool whose declared scope does not match what the route actually needs is
// a lie the agent cannot see: it reads the scope in the refusal message and
// asks for a token that still will not work.
func TestEveryToolDeclaresTheScopeItsRouteNeeds(t *testing.T) {
	for _, r := range routes(t) {
		if r.scope == "" {
			t.Errorf("%s declares no scope", r.name)
			continue
		}
		// A template path is checked as it stands: every prefix rule in
		// ScopeAllows matches before the first placeholder.
		if !auth.ScopeAllows(r.scope, r.method, r.path) {
			t.Errorf("%s declares scope %q but %s %s is not allowed by it", r.name, r.scope, r.method, r.path)
		}
		// And the scope has to be one a token can actually be given.
		if r.scope != "read" {
			found := false
			for _, s := range auth.Scopes {
				if s == r.scope {
					found = true
				}
			}
			if !found {
				t.Errorf("%s declares scope %q, which is not offered to tokens", r.name, r.scope)
			}
		}
	}
}

// Two tools with one name means the second is unreachable: the dispatch takes
// the first match.
func TestToolNamesAreUnique(t *testing.T) {
	s := &Server{}
	seen := map[string]bool{}
	for _, tool := range s.curatedTools() {
		if seen[tool.Name] {
			t.Errorf("%s is defined twice", tool.Name)
		}
		seen[tool.Name] = true
	}
}

// The description is the whole interface for a model. An empty or bare one
// makes the tool unusable however well it works.
func TestEveryToolExplainsItself(t *testing.T) {
	for _, r := range routes(t) {
		if len(r.desc) < 25 {
			t.Errorf("%s has too thin a description: %q", r.name, r.desc)
		}
		if !strings.HasSuffix(strings.TrimSpace(r.desc), ".") {
			t.Errorf("%s: description should be a sentence, got %q", r.name, r.desc)
		}
	}
}

// Path templates have to be fillable: a placeholder with no matching argument
// would be sent to the router literally.
func TestEveryPlaceholderHasAnArgument(t *testing.T) {
	s := &Server{}
	for _, tool := range s.curatedTools() {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		for _, m := range placeholder.FindAllStringSubmatch(tool.Path, -1) {
			if _, ok := props[m[1]]; !ok {
				t.Errorf("%s has {%s} in its path but no such argument", tool.Name, m[1])
			}
		}
	}
}

// A parameter the tool advertises and its handler does not read is worse than a
// missing one: the agent fills it in, the call succeeds, and the answer is
// about something else. Five tools shipped like this, and each failed in its
// own way — `diagnostics` offered `kind` where the handler reads `tool`, so
// every call was refused; `metrics_history` offered `hours` where the handler
// reads `range`, so asking for six hours quietly returned one; `search_files`
// offered `path` and `query` where the handler read `root` and `q`, and
// answered "path must be absolute" about a path that was.
//
// The names are read out of the handler the route actually reaches, one level
// into the helpers it calls, because "some handler somewhere reads this name"
// was the weaker check that let search_files through.
func TestEveryToolParameterIsOneItsHandlerReads(t *testing.T) {
	handlers := handlersByRoute(t)
	reads := queryNamesByHandler(t)
	s := &Server{}
	for _, tool := range s.curatedTools() {
		// A write carries a body, decoded into whatever struct the feature
		// package already has; those names are checked by the type system when
		// the handler is written, not here.
		if tool.Method != http.MethodGet && tool.Method != http.MethodDelete {
			continue
		}
		h := handlers[tool.Method+" "+tool.Path]
		if h == "" {
			continue // reached through a wrapper this cannot see; not a finding
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		for name := range props {
			if strings.Contains(tool.Path, "{"+name+"}") {
				continue // filled into the path, not the query
			}
			if !reads[h][name] {
				t.Errorf("%s offers %q, which %s never reads", tool.Name, name, h)
			}
		}
	}
}

// handlersByRoute maps "METHOD /path" to the handler the router sends it to.
func handlersByRoute(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	re := regexp.MustCompile(`mux\.HandleFunc\("([^"]+)",(.+)`)
	name := regexp.MustCompile(`s\.(handle\w+)`)
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		if hs := name.FindAllStringSubmatch(m[2], -1); len(hs) > 0 {
			out[m[1]] = hs[len(hs)-1][1]
		}
	}
	if len(out) < 100 {
		t.Fatalf("only %d routes parsed out of server.go; the scan is not reading what it thinks it is", len(out))
	}
	return out
}

// queryNamesByHandler collects the query keys each handler reads, including
// those read by the package functions it calls — a handler that hands the
// request to a helper is still reading them.
func queryNamesByHandler(t *testing.T) map[string]map[string]bool {
	t.Helper()
	src := ""
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		src += string(b) + "\n"
	}
	bodies := map[string]string{}
	fn := regexp.MustCompile(`(?s)\nfunc (?:\(s \*Server\) )?(\w+)\([^)]*\)[^{]*\{(.*?)\n\}\n`)
	for _, m := range fn.FindAllStringSubmatch(src, -1) {
		bodies[m[1]] += m[2]
	}
	get := regexp.MustCompile(`Get\("(\w+)"\)`)
	calls := regexp.MustCompile(`(?:s\.)?(\w+)\(`)
	out := map[string]map[string]bool{}
	for name, body := range bodies {
		if !strings.HasPrefix(name, "handle") {
			continue
		}
		seen := map[string]bool{}
		text := body
		for _, c := range calls.FindAllStringSubmatch(body, -1) {
			if inner, ok := bodies[c[1]]; ok && c[1] != name {
				text += inner
			}
		}
		for _, m := range get.FindAllStringSubmatch(text, -1) {
			seen[m[1]] = true
		}
		out[name] = seen
	}
	if len(out) < 100 {
		t.Fatalf("only %d handlers parsed; the scan is not reading what it thinks it is", len(out))
	}
	return out
}

// A description that names values the validator rejects is the same lie as a
// parameter nothing reads, one level down, and the name check cannot see it:
// create_domain offered "container, app, port, panel, static or redirect" where
// the proxy accepts three of those. An agent asked to put a site on a domain
// picked "app", was refused, and had no way to learn what would have worked.
//
// So the accepted set is asked of the validator rather than written down twice.
func TestCreateDomainOffersOnlyTargetTypesTheProxyAccepts(t *testing.T) {
	var desc string
	s := &Server{}
	for _, tool := range s.curatedTools() {
		if tool.Name != "create_domain" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		p, _ := props["targetType"].(map[string]any)
		desc, _ = p["description"].(string)
	}
	if desc == "" {
		t.Fatal("create_domain no longer describes targetType")
	}
	// The values are the words before the first sentence break.
	head, _, _ := strings.Cut(desc, ".")
	for _, word := range regexp.MustCompile(`[a-z-]+`).FindAllString(head, -1) {
		if word == "or" || word == "and" {
			continue
		}
		// A type is offered honestly if some target shape gets past Validate
		// without the type itself being the objection.
		ok := false
		for _, target := range []string{"", "web", "http://127.0.0.1:8080"} {
			d := proxy.Domain{Host: "site.example.com", TargetType: word, Target: target, Port: 8080}
			err := d.Validate()
			if err == nil || !strings.Contains(err.Error(), "targetType") {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("create_domain offers targetType %q, which the proxy refuses", word)
		}
	}
}

// The diagnostics tool answered "500 streaming unsupported" every time it was
// called: the endpoint sends server-sent events, and the recorder the tools
// call through was not an http.Flusher, so the handler refused before writing
// anything. An agent reads that as a broken server.
func TestTheRecorderCanBeFlushed(t *testing.T) {
	var w http.ResponseWriter = &bufferWriter{}
	if _, ok := w.(http.Flusher); !ok {
		t.Fatal("a handler that streams will refuse to write to this")
	}
}

// And once it can be flushed, what lands in the buffer is frames. The agent
// asked for a ping; it should read like a ping.
func TestSSEFramesComeBackAsTheTextTheyCarried(t *testing.T) {
	body := "event: line\ndata: \"PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data.\"\n\n" +
		"event: line\ndata: \"64 bytes from 1.1.1.1: icmp_seq=1 ttl=54 time=7.37 ms\"\n\n" +
		"event: end\ndata: {\"ok\":true}\n\n"
	want := "PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data.\n64 bytes from 1.1.1.1: icmp_seq=1 ttl=54 time=7.37 ms"
	if got := unwrapSSE(body); got != want {
		t.Errorf("unwrapSSE gave\n%q\nwant\n%q", got, want)
	}
	// A data line that is not a JSON string is passed through rather than
	// dropped: losing output is worse than showing it raw.
	if got := unwrapSSE("data: {\"stage\":\"build\"}\n"); got != "{\"stage\":\"build\"}" {
		t.Errorf("raw data line became %q", got)
	}
	if got := unwrapSSE("nothing here\n"); got != "" {
		t.Errorf("a body with no data lines gave %q", got)
	}
}

// Both halves of this were silent. A boolean sent as "true" is not "1", which
// is what every handler here compares against, so the flag was dropped without
// a word — search_files could never search inside files. And a JSON number
// large enough prints in exponent form, so a limit of ten million would have
// arrived as "1e+07" and parsed as nothing.
func TestQueryValuesAreWhatHandlersRead(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{true, "1"},
		{false, "0"},
		{float64(100), "100"},
		{float64(10000000), "10000000"},
		{float64(1.5), "1.5"},
		{"/var/log", "/var/log"},
	}
	for _, c := range cases {
		if got := queryValue(c.in); got != c.want {
			t.Errorf("queryValue(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
