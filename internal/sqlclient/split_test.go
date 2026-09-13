package sqlclient

import (
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"
)

// update rewrites the byte offsets in the shared corpus from this
// implementation. The text, kind, danger and parameter expectations are
// written by hand and are never regenerated: only the offsets, which are
// tedious to count and are checked by the doc[start:end] == sql assertion
// below anyway.
var update = flag.Bool("update", false, "rewrite offsets in testdata/statements.json")

const corpusPath = "testdata/statements.json"

type corpus struct {
	Note  string       `json:"note"`
	Cases []corpusCase `json:"cases"`
}

type corpusCase struct {
	Name       string           `json:"name"`
	Note       string           `json:"note,omitempty"`
	Engine     string           `json:"engine"`
	Doc        string           `json:"doc"`
	Statements []corpusStatemnt `json:"statements"`
}

type corpusStatemnt struct {
	SQL    string   `json:"sql"`
	Start  int      `json:"start"`
	End    int      `json:"end"`
	Line   int      `json:"line"`
	Kind   string   `json:"kind"`
	Danger []string `json:"danger,omitempty"`
	Params []string `json:"params,omitempty"`
}

func loadCorpus(t *testing.T) *corpus {
	t.Helper()
	b, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var c corpus
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(c.Cases) == 0 {
		t.Fatal("corpus is empty")
	}
	return &c
}

func TestSplitCorpus(t *testing.T) {
	c := loadCorpus(t)
	changed := false

	for i := range c.Cases {
		tc := &c.Cases[i]
		t.Run(tc.Name, func(t *testing.T) {
			got := Split(tc.Doc, tc.Engine)
			if len(got) != len(tc.Statements) {
				t.Fatalf("got %d statements, want %d\ngot: %s", len(got), len(tc.Statements), mustJSON(got))
			}
			for j, want := range tc.Statements {
				g := got[j]
				if g.SQL != want.SQL {
					t.Errorf("statement %d: sql\n got %q\nwant %q", j, g.SQL, want.SQL)
				}
				if string(g.Kind) != want.Kind {
					t.Errorf("statement %d (%q): kind = %q, want %q", j, g.SQL, g.Kind, want.Kind)
				}
				if !reflect.DeepEqual(dangerStrings(g.Danger), want.Danger) {
					t.Errorf("statement %d (%q): danger = %v, want %v", j, g.SQL, dangerStrings(g.Danger), want.Danger)
				}
				if !reflect.DeepEqual(nilIfEmpty(g.Params), want.Params) {
					t.Errorf("statement %d (%q): params = %v, want %v", j, g.SQL, g.Params, want.Params)
				}
				// The offsets have to select exactly the text, or the editor
				// highlights the wrong thing and the TypeScript side is
				// comparing against a lie.
				if g.Start < 0 || g.End > len(tc.Doc) || g.Start > g.End {
					t.Fatalf("statement %d: range [%d,%d) is outside the document", j, g.Start, g.End)
				}
				if tc.Doc[g.Start:g.End] != g.SQL {
					t.Errorf("statement %d: doc[%d:%d] = %q, not the statement text", j, g.Start, g.End, tc.Doc[g.Start:g.End])
				}
				if *update {
					if want.Start != g.Start || want.End != g.End || want.Line != g.Line {
						changed = true
					}
					tc.Statements[j].Start, tc.Statements[j].End, tc.Statements[j].Line = g.Start, g.End, g.Line
					continue
				}
				if g.Start != want.Start || g.End != want.End {
					t.Errorf("statement %d (%q): range = [%d,%d), want [%d,%d)", j, g.SQL, g.Start, g.End, want.Start, want.End)
				}
				if g.Line != want.Line {
					t.Errorf("statement %d (%q): line = %d, want %d", j, g.SQL, g.Line, want.Line)
				}
			}
		})
	}

	if *update {
		b, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(corpusPath, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		if changed {
			t.Log("offsets rewritten in " + corpusPath)
		}
	}
}

// TestSplitRangesAreOrderedAndDisjoint guards the property the editor relies
// on: statements come back in document order and never overlap.
func TestSplitRangesAreOrderedAndDisjoint(t *testing.T) {
	for _, tc := range loadCorpus(t).Cases {
		prev := -1
		for _, st := range Split(tc.Doc, tc.Engine) {
			if st.Start < prev {
				t.Errorf("%s: statement at %d starts before the previous one ended at %d", tc.Name, st.Start, prev)
			}
			prev = st.End
		}
	}
}

func dangerStrings(d []Danger) []string {
	if len(d) == 0 {
		return nil
	}
	out := make([]string, len(d))
	for i, x := range d {
		out[i] = string(x)
	}
	return out
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func mustJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
