package sqlclient

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// bindNamed rewrites :name placeholders into the engine's own placeholder and
// returns the arguments in the order the driver expects them.
//
// Values are bound by the driver and never interpolated into the string. That
// is the whole point of the parameter feature: a saved query with a parameter
// is the correct answer to injection, and a saved query that pasted its
// parameter into the text would be the opposite.
func bindNamed(stmt string, d Dialect, params map[string]any) (string, []any, error) {
	lx := newLexer(stmt, d)
	type slot struct {
		start, end int
		name       string
	}
	var slots []slot
	for {
		t, ok := lx.next()
		if !ok {
			break
		}
		if t.kind == tokParam && strings.HasPrefix(t.text, ":") {
			slots = append(slots, slot{t.start, t.end, t.text[1:]})
		}
	}
	if len(slots) == 0 {
		return stmt, nil, nil
	}

	var missing []string
	for _, s := range slots {
		if _, ok := params[s.name]; !ok {
			missing = append(missing, ":"+s.name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		missing = dedupe(missing)
		return "", nil, fmt.Errorf("no value given for %s", strings.Join(missing, ", "))
	}

	var (
		out  strings.Builder
		args []any
		// Postgres numbers its placeholders, so a name used twice is bound
		// once and referenced twice. MySQL cannot do that and gets the value
		// again for each occurrence.
		index = map[string]int{}
		prev  = 0
	)
	out.Grow(len(stmt))
	for _, s := range slots {
		out.WriteString(stmt[prev:s.start])
		switch d {
		case MySQL:
			out.WriteByte('?')
			args = append(args, params[s.name])
		default:
			n, seen := index[s.name]
			if !seen {
				args = append(args, params[s.name])
				n = len(args)
				index[s.name] = n
			}
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(n))
		}
		prev = s.end
	}
	out.WriteString(stmt[prev:])
	return out.String(), args, nil
}

func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
