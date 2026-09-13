package sqlclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Target is the connection a run is aimed at, and the two facts about it that
// change what is allowed: read-only refuses writes, production makes every
// write ask first.
type Target struct {
	Ref      string // instance name or saved connection id
	User     string // the Islet user, for the pool key and the audit trail
	Engine   string
	ReadOnly bool
	Config   Config
}

// RunOptions are the knobs one press of Run carries.
type RunOptions struct {
	RowCap          int           // 0 means the default; clamped to MaxRowCap
	Timeout         time.Duration // 0 means the default; clamped to MaxTimeout
	Transaction     bool          // wrap the run and leave the transaction open
	ContinueOnError bool          // by default a failed statement stops the batch
	Params          map[string]any
	Confirmed       bool // the interface collected the confirmation for flagged statements
}

// StatementResult is one message of the run's stream: one statement, reported
// as soon as it finishes rather than at the end of the batch.
type StatementResult struct {
	Index        int         `json:"index"`
	SQL          string      `json:"sql"`
	Start        int         `json:"start"` // byte offsets into the document, for highlighting
	End          int         `json:"end"`
	Line         int         `json:"line"`
	Kind         Kind        `json:"kind"`
	Danger       []Danger    `json:"danger,omitempty"`
	Columns      []Column    `json:"columns,omitempty"`
	Rows         [][]Value   `json:"rows,omitempty"`
	RowCount     int         `json:"rowCount"`
	Truncated    bool        `json:"truncated"`
	RowsAffected int64       `json:"rowsAffected"` // -1 when the engine did not say
	DurationMS   int64       `json:"durationMs"`
	Error        *QueryError `json:"error,omitempty"`
}

// Summary closes the stream.
type Summary struct {
	RunID         string `json:"runId"`
	Statements    int    `json:"statements"`
	Failed        int    `json:"failed"`
	DurationMS    int64  `json:"durationMs"`
	SchemaChanged bool   `json:"schemaChanged"` // the caller drops its introspection cache
	InTransaction bool   `json:"inTransaction"`
}

// Preflight splits a document and refuses the whole thing if any statement in
// it is not allowed. Refusing before the first statement runs is the
// difference between a rejected batch and half an applied one.
func Preflight(doc string, t Target, opt RunOptions) ([]Statement, error) {
	stmts := Split(doc, t.Engine)
	if len(stmts) == 0 {
		return nil, errors.New("nothing to run")
	}
	for _, st := range stmts {
		if t.ReadOnly && !st.AllowedReadOnly() {
			return nil, fmt.Errorf("%w: %s", ErrReadOnly, firstWords(st.SQL))
		}
		if !opt.Confirmed && st.NeedsConfirmation() {
			return nil, fmt.Errorf("%w: %s", ErrNeedsConfirmation, firstWords(st.SQL))
		}
	}
	return stmts, nil
}

// Execute runs a document, one statement at a time, calling emit as each one
// finishes.
//
// When sess is non-nil the statements run on that tab's dedicated connection,
// so SET and an open transaction survive to the next run. Otherwise a pooled
// connection is borrowed for the batch and given back at the end.
func (m *Manager) Execute(ctx context.Context, runID string, t Target, sess *Session, doc string, opt RunOptions, emit func(StatementResult) error) (Summary, error) {
	stmts, err := Preflight(doc, t, opt)
	if err != nil {
		return Summary{RunID: runID}, err
	}
	if opt.Transaction && sess == nil {
		return Summary{RunID: runID}, errors.New("transaction mode needs a session: open a tab first")
	}

	if sess != nil {
		if err := sess.begin(); err != nil {
			return Summary{RunID: runID}, err
		}
		defer sess.end()
		sum, err := m.execOn(ctx, runID, t, sess, sess.conn, stmts, opt, emit)
		return sum, err
	}

	var sum Summary
	err = m.Use(ctx, t.Ref, t.User, t.Config, func(conn Conn) error {
		var e error
		sum, e = m.execOn(ctx, runID, t, nil, conn, stmts, opt, emit)
		return e
	})
	return sum, err
}

func (m *Manager) execOn(ctx context.Context, runID string, t Target, sess *Session, conn Conn, stmts []Statement, opt RunOptions, emit func(StatementResult) error) (Summary, error) {
	started := time.Now()
	sum := Summary{RunID: runID, Statements: len(stmts)}

	if opt.Transaction && sess != nil && !sess.InTransaction() {
		res := m.runOne(ctx, runID, t, conn, Statement{SQL: "BEGIN", Kind: KindTransaction}, -1, opt)
		if res.Error != nil {
			sum.DurationMS = time.Since(started).Milliseconds()
			sum.Failed = 1
			return sum, res.Error
		}
		sess.setInTx(true)
	}

	for i, st := range stmts {
		res := m.runOne(ctx, runID, t, conn, st, i, opt)
		if res.Error != nil {
			sum.Failed++
		}
		if st.Kind == KindDDL {
			sum.SchemaChanged = true
		}
		if sess != nil {
			trackTransaction(sess, st, res.Error == nil)
		}
		if err := emit(res); err != nil {
			sum.DurationMS = time.Since(started).Milliseconds()
			return sum, err // the client went away; stop rather than keep running
		}
		if res.Error != nil && !opt.ContinueOnError {
			sum.Statements = i + 1
			break
		}
		if ctx.Err() != nil {
			sum.Statements = i + 1
			break
		}
	}

	sum.DurationMS = time.Since(started).Milliseconds()
	if sess != nil {
		sum.InTransaction = sess.InTransaction()
	}
	return sum, nil
}

// runOne executes a single statement and collects at most the row cap.
func (m *Manager) runOne(ctx context.Context, runID string, t Target, conn Conn, st Statement, index int, opt RunOptions) StatementResult {
	res := StatementResult{
		Index: index, SQL: st.SQL, Start: st.Start, End: st.End, Line: st.Line,
		Kind: st.Kind, Danger: st.Danger, RowsAffected: -1,
	}

	sql, args, err := bindNamed(st.SQL, dialectOf(t.Engine), opt.Params)
	if err != nil {
		res.Error = &QueryError{Message: err.Error()}
		return res
	}

	rowCap := ClampRowCap(opt.RowCap)
	runCtx, cancel := context.WithTimeout(ctx, ClampTimeout(opt.Timeout))
	defer cancel()
	untrack := m.track(runID, t.User, conn, cancel)
	defer untrack()

	started := time.Now()
	cur, err := conn.Run(runCtx, sql, args, st.ReturnsRows)
	if err != nil {
		res.DurationMS = time.Since(started).Milliseconds()
		res.Error = describe(err, runCtx, ctx, ClampTimeout(opt.Timeout))
		return res
	}
	defer cur.Close()

	res.Columns = cur.Columns()
	// The cursor is always advanced, even when there are no columns to read.
	// A driver may hand back a cursor whose failure only appears once it is
	// asked for a row: pgx does exactly that for a statement the server
	// cancelled, and skipping the loop for a statement with no result set
	// turned a killed query into a silent success.
	//
	// Bounded by construction: the loop stops at the cap and the extra Next
	// only asks whether there was one more, it does not keep it.
	rows := make([][]Value, 0, min(rowCap, 512))
	for len(rows) < rowCap && cur.Next() {
		v, err := cur.Values()
		if err != nil {
			res.Error = asQueryError(err)
			break
		}
		rows = append(rows, v)
	}
	if res.Error == nil && len(rows) == rowCap && cur.Next() {
		res.Truncated = true
	}
	if len(res.Columns) > 0 {
		res.Rows = rows
		res.RowCount = len(rows)
	}
	if err := cur.Err(); err != nil && res.Error == nil {
		res.Error = describe(err, runCtx, ctx, ClampTimeout(opt.Timeout))
	}
	res.RowsAffected = cur.RowsAffected()
	res.DurationMS = time.Since(started).Milliseconds()
	return res
}

// describe turns a failure into something the user can act on. A timeout and a
// cancel both arrive as a context error and mean very different things.
func describe(err error, runCtx, parent context.Context, limit time.Duration) *QueryError {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return &QueryError{
			Message: fmt.Sprintf("the statement passed its %s timeout and was cancelled on the server", limit),
			Code:    "timeout",
		}
	}
	if errors.Is(parent.Err(), context.Canceled) {
		return &QueryError{Message: "cancelled", Code: "cancelled"}
	}
	if errors.Is(err, context.Canceled) || errors.Is(runCtx.Err(), context.Canceled) {
		return &QueryError{Message: "cancelled", Code: "cancelled"}
	}
	return asQueryError(err)
}

// trackTransaction keeps the session's idea of an open transaction in step
// with what the user actually ran, so the status bar is not a guess.
func trackTransaction(s *Session, st Statement, ok bool) {
	if !ok || st.Kind != KindTransaction {
		return
	}
	fields := strings.Fields(st.SQL)
	if len(fields) == 0 {
		return
	}
	verb := strings.ToLower(fields[0])
	switch verb {
	case "begin", "start":
		s.setInTx(true)
	case "commit", "rollback", "end", "abort":
		s.setInTx(false)
	}
}

// firstWords is a short prefix of a statement, for an error message that names
// what was refused without quoting somebody's whole script back at them.
func firstWords(sql string) string {
	sql = strings.Join(strings.Fields(sql), " ")
	if len(sql) > 60 {
		return sql[:57] + "..."
	}
	return sql
}
