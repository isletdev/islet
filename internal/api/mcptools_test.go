package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/isletdev/islet/internal/auth"
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

// A parameter the tool advertises and no handler reads is worse than a missing
// one: the agent fills it in, the call succeeds, and the answer is about
// something else. Both known cases were exactly that — `diagnostics` offered
// `kind` where the handler reads `tool`, so every call was refused; and
// `metrics_history` offered `hours` where the handler reads `range`, so asking
// for six hours quietly returned one.
//
// So the check is crude on purpose: every name a tool offers has to be a name
// this package reads from a query or a path, or a field pkg/api decodes from a
// body. Source is scanned because that is where the truth is — the alternative
// is a second table that drifts from the first.
func TestEveryToolParameterIsOneSomethingReads(t *testing.T) {
	read := namesTheAPIReads(t)
	s := &Server{}
	for _, tool := range s.curatedTools() {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		for name := range props {
			if !read[name] {
				t.Errorf("%s offers %q, which nothing in the daemon reads", tool.Name, name)
			}
		}
	}
}

// namesTheAPIReads collects every query key, path placeholder and JSON field
// the daemon takes in. It walks the whole module because a request body is
// decoded into whatever struct the feature package already has — pkg/api is
// only where the shared shapes live.
func namesTheAPIReads(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\.Get\("(\w+)"\)`),
		regexp.MustCompile(`PathValue\("(\w+)"\)`),
		regexp.MustCompile("json:\"(\\w+)"),
	}
	for _, root := range []string{"../../internal", "../../pkg"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, re := range patterns {
				for _, m := range re.FindAllSubmatch(b, -1) {
					out[string(m[1])] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(out) < 200 {
		t.Fatalf("only %d names found; the scan is not reading the source it thinks it is", len(out))
	}
	return out
}
