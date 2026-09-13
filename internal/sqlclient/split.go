package sqlclient

import "strings"

// Statement is one statement found in a document, with everything the editor
// and the safety layer need to know about it.
//
// Start and End are byte offsets into the document, and SQL is exactly
// doc[Start:End]: the terminating semicolon and any surrounding whitespace or
// comment are outside the range. Bytes, not runes and not UTF-16 code units,
// because Go slices bytes; the frontend converts, and the shared fixture in
// testdata/statements.json records byte offsets so the two implementations are
// compared on the same numbers.
type Statement struct {
	SQL   string `json:"sql"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Line  int    `json:"line"` // 1-based line of Start, for error messages
	Kind  Kind   `json:"kind"`
	// ReturnsRows is whether a result set is expected. Postgres works this
	// out for itself; MySQL has to be told, because asking for rows from an
	// INSERT there loses the affected-row count.
	ReturnsRows bool     `json:"returnsRows"`
	Danger      []Danger `json:"danger,omitempty"`
	Params      []string `json:"params,omitempty"` // :name placeholders, in first-seen order
}

// Split breaks a document into statements for the given engine.
//
// It does not split on semicolons. Semicolons inside string literals, quoted
// identifiers, dollar-quoted function bodies and comments are part of the text
// around them, which is the difference between running a trigger function and
// running the first three lines of one. A trailing statement without a
// semicolon is a statement; an empty one is not.
func Split(doc, engine string) []Statement {
	d := dialectOf(engine)
	lx := newLexer(doc, d)

	var out []Statement
	var toks []token
	lineAt := newLineIndex(doc)

	flush := func() {
		if len(toks) == 0 {
			return
		}
		start, end := toks[0].start, toks[len(toks)-1].end
		st := Statement{
			SQL:    doc[start:end],
			Start:  start,
			End:    end,
			Line:   lineAt(start),
			Params: paramNames(toks),
		}
		st.Kind, st.Danger = classify(toks)
		st.ReturnsRows = returnsRows(toks, st.Kind)
		out = append(out, st)
		toks = toks[:0]
	}

	for {
		t, ok := lx.next()
		if !ok {
			break
		}
		if t.kind == tokSemicolon {
			flush()
			continue
		}
		toks = append(toks, t)
	}
	flush()
	if out == nil {
		return []Statement{}
	}
	return out
}

// paramNames returns the distinct :name placeholders in order of first
// appearance. Positional forms ($1, ?) are the driver's business, not the
// parameter form's, so they are not reported here.
func paramNames(toks []token) []string {
	var names []string
	seen := map[string]bool{}
	for _, t := range toks {
		if t.kind != tokParam || !strings.HasPrefix(t.text, ":") {
			continue
		}
		n := t.text[1:]
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	return names
}

// newLineIndex returns a function from byte offset to 1-based line number.
// Offsets are looked up in increasing order in practice, so a cursor beats a
// binary search and a rescan beats both for the sizes involved.
func newLineIndex(doc string) func(int) int {
	starts := []int{0}
	for i := 0; i < len(doc); i++ {
		if doc[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	cur := 0
	return func(off int) int {
		if off < starts[cur] {
			cur = 0
		}
		for cur+1 < len(starts) && starts[cur+1] <= off {
			cur++
		}
		return cur + 1
	}
}
