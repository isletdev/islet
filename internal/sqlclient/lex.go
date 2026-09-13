package sqlclient

import "strings"

// Dialect selects the lexical rules that differ between engines. They differ
// more than people expect, and every difference below is a way to mis-split a
// document and run half a statement.
type Dialect string

const (
	// Postgres nests block comments, dollar-quotes function bodies, and takes
	// backslash escapes only inside E'...'.
	Postgres Dialect = "postgres"
	// MySQL quotes identifiers with backticks, treats " as a string by
	// default, takes backslash escapes everywhere, has # comments, requires a
	// space after -- , and does not nest block comments.
	MySQL Dialect = "mysql"
)

// dialectOf maps an engine name onto its lexical rules. MariaDB is MySQL here.
func dialectOf(engine string) Dialect {
	switch strings.ToLower(engine) {
	case "mysql", "mariadb":
		return MySQL
	default:
		return Postgres
	}
}

type tokenKind uint8

const (
	tokWord        tokenKind = iota // bare identifier or keyword
	tokQuotedIdent                  // "x" in Postgres, `x` in MySQL
	tokString                       // '...', $tag$...$tag$, and MySQL's "..."
	tokNumber
	tokParam     // :name, $1, ?
	tokPunct     // one operator or punctuation character
	tokSemicolon // the statement terminator, the only reason this exists
)

// token is one lexical item. Offsets are byte offsets into the document the
// lexer was given; see the note on Statement about why bytes and not runes.
type token struct {
	kind  tokenKind
	text  string // exact slice of the document
	lower string // lowercased, filled for tokWord only
	start int
	end   int
	depth int // parenthesis nesting at this token, so a subquery is visible
}

// isWord reports whether the token is the bare keyword kw. A quoted
// identifier never matches: "select" is a column called select.
func (t token) isWord(kw string) bool { return t.kind == tokWord && t.lower == kw }

// lexer walks a document once and hands out tokens. Comments and whitespace
// are skipped, which is what makes "an UPDATE inside a comment" a non-event
// for the classifier: it never sees one.
type lexer struct {
	src     string
	dialect Dialect
	pos     int
	depth   int
}

func newLexer(src string, d Dialect) *lexer { return &lexer{src: src, dialect: d} }

// next returns the next token and true, or the zero token and false at the end
// of the document.
func (l *lexer) next() (token, bool) {
	l.skipGaps()
	if l.pos >= len(l.src) {
		return token{}, false
	}
	start := l.pos
	c := l.src[l.pos]

	switch {
	case c == ';':
		l.pos++
		return l.emit(tokSemicolon, start), true

	case c == '(':
		l.pos++
		t := l.emit(tokPunct, start)
		l.depth++
		return t, true

	case c == ')':
		l.pos++
		if l.depth > 0 {
			l.depth--
		}
		return l.emit(tokPunct, start), true

	case c == '\'':
		l.scanQuoted('\'', l.dialect == MySQL)
		return l.emit(tokString, start), true

	case c == '"':
		// Postgres: a quoted identifier. MySQL: a string, unless the server
		// runs with ANSI_QUOTES, which we cannot know from here and which
		// changes nothing about where the string ends.
		l.scanQuoted('"', l.dialect == MySQL)
		if l.dialect == MySQL {
			return l.emit(tokString, start), true
		}
		return l.emit(tokQuotedIdent, start), true

	case c == '`' && l.dialect == MySQL:
		l.scanQuoted('`', false)
		return l.emit(tokQuotedIdent, start), true

	case c == '$' && l.dialect == Postgres:
		if tag, ok := l.dollarTag(l.pos); ok {
			l.scanDollarBody(tag)
			return l.emit(tokString, start), true
		}
		// $1, $2: a positional parameter.
		l.pos++
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.pos++
		}
		return l.emit(tokParam, start), true

	case c == '?':
		l.pos++
		return l.emit(tokParam, start), true

	case c == ':' && l.pos+1 < len(l.src) && l.src[l.pos+1] == ':':
		// Postgres's cast operator. Consumed whole so that the second colon
		// cannot start a parameter: x::int is a cast, not a bind of :int.
		l.pos += 2
		return l.emit(tokPunct, start), true

	case c == ':' && l.pos+1 < len(l.src) && isIdentStart(l.src[l.pos+1]):
		// :name, the placeholder syntax saved queries declare.
		l.pos++
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
		return l.emit(tokParam, start), true

	case isDigit(c):
		for l.pos < len(l.src) && (isIdentPart(l.src[l.pos]) || l.src[l.pos] == '.') {
			l.pos++
		}
		return l.emit(tokNumber, start), true

	case isIdentStart(c):
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
		word := l.src[start:l.pos]
		// E'...' and friends: the prefix belongs to the string, not to a word.
		if l.pos < len(l.src) && l.src[l.pos] == '\'' && isStringPrefix(word, l.dialect) {
			l.scanQuoted('\'', l.dialect == MySQL || strings.EqualFold(word, "e"))
			return l.emit(tokString, start), true
		}
		t := l.emit(tokWord, start)
		t.lower = strings.ToLower(word)
		return t, true

	default:
		l.pos++
		return l.emit(tokPunct, start), true
	}
}

func (l *lexer) emit(k tokenKind, start int) token {
	return token{kind: k, text: l.src[start:l.pos], start: start, end: l.pos, depth: l.depth}
}

// skipGaps advances past whitespace and comments.
func (l *lexer) skipGaps() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			l.pos++
		case c == '-' && l.lineCommentAt(l.pos):
			l.skipToEOL()
		case c == '#' && l.dialect == MySQL:
			l.skipToEOL()
		case c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*':
			l.skipBlockComment()
		default:
			return
		}
	}
}

// lineCommentAt reports whether a -- comment starts at i. MySQL requires
// whitespace after the second dash, so a--b is an expression there and a
// comment in Postgres.
func (l *lexer) lineCommentAt(i int) bool {
	if i+1 >= len(l.src) || l.src[i+1] != '-' {
		return false
	}
	if l.dialect != MySQL {
		return true
	}
	if i+2 >= len(l.src) {
		return true // end of document: nothing left to be an operand
	}
	switch l.src[i+2] {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

func (l *lexer) skipToEOL() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
	}
}

// skipBlockComment consumes /* ... */. Postgres nests them, which is the whole
// point of the /* a /* b */ c */ case in the test corpus: a non-nesting scanner
// stops at the first */ and then reads "c */" as SQL.
func (l *lexer) skipBlockComment() {
	nest := 0
	for l.pos < len(l.src) {
		if l.src[l.pos] == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*' {
			nest++
			l.pos += 2
			if l.dialect == MySQL && nest > 1 {
				nest = 1 // MySQL does not nest: the inner /* is just text
			}
			continue
		}
		if l.src[l.pos] == '*' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
			l.pos += 2
			nest--
			if nest <= 0 {
				return
			}
			continue
		}
		l.pos++
	}
}

// scanQuoted consumes a quoted run starting at the opening quote. A doubled
// quote is an escaped quote in every dialect; a backslash escapes the next
// character only where backslash is true. An unterminated run consumes to the
// end of the document, which is correct: the user is still typing.
func (l *lexer) scanQuoted(q byte, backslash bool) {
	l.pos++ // opening quote
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case backslash && c == '\\' && l.pos+1 < len(l.src):
			l.pos += 2
		case c == q:
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == q {
				l.pos += 2 // '' is one quote, not the end
				continue
			}
			l.pos++
			return
		default:
			l.pos++
		}
	}
}

// dollarTag matches $$ or $tag$ at i and returns the full delimiter.
func (l *lexer) dollarTag(i int) (string, bool) {
	j := i + 1
	// A tag is an identifier, and $ is not part of one here: without that
	// rule $$ swallows its own closing delimiter.
	for j < len(l.src) && (isAlpha(l.src[j]) || l.src[j] == '_' || l.src[j] >= 0x80 || (j > i+1 && isDigit(l.src[j]))) {
		j++
	}
	if j < len(l.src) && l.src[j] == '$' {
		return l.src[i : j+1], true
	}
	return "", false
}

// scanDollarBody consumes from the opening delimiter to the matching close.
func (l *lexer) scanDollarBody(tag string) {
	l.pos += len(tag)
	if k := strings.Index(l.src[l.pos:], tag); k >= 0 {
		l.pos += k + len(tag)
		return
	}
	l.pos = len(l.src) // unterminated: still being typed
}

// isStringPrefix reports whether word immediately before a quote makes the
// quoted run a string literal rather than an identifier next to a string.
func isStringPrefix(word string, d Dialect) bool {
	switch strings.ToLower(word) {
	case "e", "u&":
		return d == Postgres
	case "b", "x", "n", "_binary", "_utf8", "_utf8mb4":
		return true
	}
	return false
}

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool { return isAlpha(c) || c == '_' || c >= 0x80 }
func isIdentPart(c byte) bool  { return isIdentStart(c) || isDigit(c) || c == '$' }
func isAlpha(c byte) bool      { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
