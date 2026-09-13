package sqlclient

// Kind is what a statement does, at the coarseness the safety layer needs.
type Kind string

const (
	KindRead        Kind = "read"        // returns rows and changes nothing
	KindWrite       Kind = "write"       // changes rows
	KindDDL         Kind = "ddl"         // changes the schema; invalidates the introspection cache
	KindTransaction Kind = "transaction" // begin, commit, rollback, savepoint
	KindSession     Kind = "session"     // set, reset, use, discard
	KindUtility     Kind = "utility"     // vacuum, analyze, checkpoint, lock
	KindUnknown     Kind = "unknown"     // treated as a write, because guessing low is how data dies
)

// Danger is a reason a statement needs an explicit confirmation before it runs.
type Danger string

const (
	DangerUnfiltered Danger = "unfiltered" // UPDATE or DELETE with no WHERE of its own
	DangerDrop       Danger = "drop"
	DangerTruncate   Danger = "truncate"
	DangerAlter      Danger = "alter"
	DangerGrant      Danger = "grant" // grant, revoke: changes who can do what
)

// Writes reports whether the statement changes data or schema. Unknown counts
// as a write: an unrecognised verb on a production connection should ask.
func (s Statement) Writes() bool {
	switch s.Kind {
	case KindRead, KindTransaction, KindSession:
		return false
	case KindUtility:
		return false
	default:
		return true
	}
}

// AllowedReadOnly reports whether a connection marked read-only may run this.
// Enforced in the daemon; the interface merely reflects it.
func (s Statement) AllowedReadOnly() bool {
	switch s.Kind {
	case KindRead, KindTransaction:
		return true
	default:
		return false
	}
}

// NeedsConfirmation reports whether the interface must confirm before running,
// naming what will happen. Any write at all on a production connection asks.
func (s Statement) NeedsConfirmation(production bool) bool {
	if len(s.Danger) > 0 {
		return true
	}
	return production && s.Writes()
}

// verbs that begin a statement, mapped to what they do. Anything not here is
// KindUnknown and is therefore treated as a write.
var verbKind = map[string]Kind{
	"select": KindRead, "table": KindRead, "values": KindRead,
	"show": KindRead, "describe": KindRead, "desc": KindRead, "explain": KindRead,

	"insert": KindWrite, "update": KindWrite, "delete": KindWrite,
	"merge": KindWrite, "replace": KindWrite, "upsert": KindWrite,
	"call": KindWrite, "do": KindWrite, "import": KindWrite, "load": KindWrite,

	"create": KindDDL, "alter": KindDDL, "drop": KindDDL, "truncate": KindDDL,
	"comment": KindDDL, "grant": KindDDL, "revoke": KindDDL, "rename": KindDDL,
	"reindex": KindDDL, "refresh": KindDDL, "cluster": KindDDL, "security": KindDDL,

	"begin": KindTransaction, "start": KindTransaction, "commit": KindTransaction,
	"rollback": KindTransaction, "end": KindTransaction, "abort": KindTransaction,
	"savepoint": KindTransaction, "release": KindTransaction,

	"set": KindSession, "reset": KindSession, "use": KindSession, "discard": KindSession,

	"vacuum": KindUtility, "analyze": KindUtility, "analyse": KindUtility,
	"checkpoint": KindUtility, "lock": KindUtility, "unlock": KindUtility,
	"prepare": KindUtility, "deallocate": KindUtility, "execute": KindUnknown,
}

// options that may follow EXPLAIN before the statement it explains.
var explainOptions = map[string]bool{
	"analyze": true, "analyse": true, "verbose": true, "costs": true, "settings": true,
	"generic_plan": true, "buffers": true, "serialize": true, "wal": true, "timing": true,
	"summary": true, "memory": true, "format": true, "json": true, "text": true,
	"xml": true, "yaml": true, "on": true, "off": true, "true": true, "false": true,
	"extended": true, "partitions": true, "for": true, "connection": true,
}

// classify decides what a statement does and why it might need a confirmation.
// The tokens have already had comments and whitespace removed, which is why an
// UPDATE written inside a comment cannot reach this function.
func classify(toks []token) (Kind, []Danger) {
	i, verb := firstWord(toks, 0)
	if verb == "" {
		return KindUnknown, nil
	}

	// WITH may front anything. A CTE that writes makes the whole statement a
	// write, however innocent the outer SELECT looks.
	if verb == "with" {
		if j, v := dmlAnywhere(toks); v != "" {
			i, verb = j, v
		} else {
			return KindRead, nil
		}
	}

	// EXPLAIN is a read unless it was asked to run the thing, in which case it
	// is whatever it was asked to run.
	if verb == "explain" {
		j, inner, analyze := afterExplain(toks, i)
		if inner == "" || !analyze {
			return KindRead, nil
		}
		i, verb = j, inner
	}

	kind, ok := verbKind[verb]
	if !ok {
		kind = KindUnknown
	}

	// COPY runs both ways and only one of them writes.
	if verb == "copy" {
		kind = KindRead
		if hasWordAtTop(toks, i, "from") {
			kind = KindWrite
		}
		return kind, nil
	}

	var danger []Danger
	switch verb {
	case "update", "delete":
		// A WHERE inside a subquery filters the subquery, not the target
		// table, so only a WHERE at the statement's own parenthesis depth
		// counts. LIMIT does not count: a bounded delete is still a delete
		// someone should read twice.
		if !hasWordAtTop(toks, i, "where") {
			danger = append(danger, DangerUnfiltered)
		}
	case "drop":
		danger = append(danger, DangerDrop)
	case "truncate":
		danger = append(danger, DangerTruncate)
	case "alter":
		danger = append(danger, DangerAlter)
	case "grant", "revoke":
		danger = append(danger, DangerGrant)
	}
	return kind, danger
}

// firstWord returns the index and lowercase text of the first bare word at or
// after i. A quoted identifier is not a word, so a table called "select" does
// not turn a statement into a read.
func firstWord(toks []token, i int) (int, string) {
	for ; i < len(toks); i++ {
		if toks[i].kind == tokWord {
			return i, toks[i].lower
		}
	}
	return -1, ""
}

// dmlAnywhere finds the first data-modifying verb at any depth, which is how a
// writing CTE is caught.
func dmlAnywhere(toks []token) (int, string) {
	for i, t := range toks {
		if t.kind != tokWord {
			continue
		}
		switch t.lower {
		case "insert", "update", "delete", "merge":
			return i, t.lower
		}
	}
	return -1, ""
}

// afterExplain skips EXPLAIN's options and returns the index and verb of the
// statement being explained, and whether ANALYZE was among the options, which
// is the difference between planning a DELETE and performing one.
func afterExplain(toks []token, i int) (int, string, bool) {
	analyze := false
	for j := i + 1; j < len(toks); j++ {
		t := toks[j]
		if t.kind == tokPunct && (t.text == "(" || t.text == ")" || t.text == ",") {
			continue
		}
		if t.kind != tokWord {
			continue
		}
		if explainOptions[t.lower] {
			if t.lower == "analyze" || t.lower == "analyse" {
				analyze = true
			}
			continue
		}
		return j, t.lower, analyze
	}
	return -1, "", analyze
}

// hasWordAtTop reports whether kw appears after i at the same parenthesis
// depth as the statement's verb.
func hasWordAtTop(toks []token, i int, kw string) bool {
	if i < 0 || i >= len(toks) {
		return false
	}
	top := toks[i].depth
	for j := i + 1; j < len(toks); j++ {
		if toks[j].depth == top && toks[j].isWord(kw) {
			return true
		}
	}
	return false
}

// returnsRows reports whether a result set is expected back. MySQL needs this
// decided before the statement runs: asking for rows from an INSERT there
// throws away the affected-row count, and asking for an affected-row count
// from a SELECT throws away the rows.
func returnsRows(toks []token, kind Kind) bool {
	switch kind {
	case KindRead, KindUnknown:
		return true
	}
	// MariaDB and Postgres both return rows from a DML statement that asks.
	for _, t := range toks {
		if t.isWord("returning") {
			return true
		}
	}
	return false
}
