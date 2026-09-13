package sqlclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The operations the HTTP edge calls, gathered here so that the edge stays a
// matter of validating, authorising and marshalling.

// EngineSupported reports whether this client can open an engine, and says
// where to go if it cannot. Redis and Mongo are not oversights: every feature
// in this package assumes tables, columns and foreign keys.
func EngineSupported(engine string) (Dialect, error) {
	switch strings.ToLower(engine) {
	case "postgres", "postgresql":
		return Postgres, nil
	case "mysql", "mariadb":
		return MySQL, nil
	}
	return "", fmt.Errorf("the SQL client does not open %s: it is built around tables, columns and foreign keys. Adminer, in the catalog, is the answer for that one", engine)
}

// ConnectionInfo is what a connection test reports back.
type ConnectionInfo struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version"`
	LatencyMS int64  `json:"latencyMs"`
}

// Test connects, reads the server version, and disconnects. It uses the
// stored secret server-side; the browser never sees a password, not even a
// masked one.
func (m *Manager) Test(ctx context.Context, ref, user string, cfg Config) (*ConnectionInfo, error) {
	if _, err := EngineSupported(cfg.Engine); err != nil {
		return nil, err
	}
	started := time.Now()
	info := &ConnectionInfo{}
	err := m.Use(ctx, ref, user, cfg, func(conn Conn) error {
		v, err := conn.Version(ctx)
		if err != nil {
			return err
		}
		info.Version, info.OK = v, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	info.LatencyMS = time.Since(started).Milliseconds()
	return info, nil
}

// Introspect reads a connection's whole schema. The caller holds the cache;
// this is the miss path.
func (m *Manager) Introspect(ctx context.Context, t Target) (*Tree, error) {
	in, err := introspectorFor(t.Engine)
	if err != nil {
		return nil, err
	}
	var tree *Tree
	err = m.Use(ctx, t.Ref, t.User, t.Config, func(conn Conn) error {
		tree, err = in.Tree(ctx, conn)
		return err
	})
	return tree, err
}

// TableDetail reads one table's columns, indexes, constraints and foreign keys
// in both directions.
func (m *Manager) TableDetail(ctx context.Context, t Target, schema, table string) (*TableDetail, error) {
	in, err := introspectorFor(t.Engine)
	if err != nil {
		return nil, err
	}
	var detail *TableDetail
	err = m.Use(ctx, t.Ref, t.User, t.Config, func(conn Conn) error {
		detail, err = in.Table(ctx, conn, schema, table)
		return err
	})
	return detail, err
}

// Plan is a query plan, as JSON where the engine will produce it and as text
// where it will not.
type Plan struct {
	Engine     string `json:"engine"`
	Statement  string `json:"statement"`
	Analyzed   bool   `json:"analyzed"`
	JSON       any    `json:"json,omitempty"`
	Text       string `json:"text,omitempty"`
	DurationMS int64  `json:"durationMs"`
}

// Explain runs EXPLAIN over the first statement in doc.
//
// analyze is a separate argument, and a separate click in the interface,
// because EXPLAIN ANALYZE executes the statement. It goes through the same
// preflight as a run for exactly that reason: explaining a DELETE with ANALYZE
// deletes the rows.
func (m *Manager) Explain(ctx context.Context, t Target, sess *Session, doc string, analyze bool) (*Plan, error) {
	stmts := Split(doc, t.Engine)
	if len(stmts) == 0 {
		return nil, fmt.Errorf("nothing to explain")
	}
	st := stmts[0]

	if analyze {
		if t.ReadOnly && !st.AllowedReadOnly() {
			return nil, fmt.Errorf("%w: EXPLAIN ANALYZE would run %s", ErrReadOnly, firstWords(st.SQL))
		}
		if st.NeedsConfirmation(t.Production) {
			return nil, fmt.Errorf("%w: EXPLAIN ANALYZE runs the statement, and this one is %s", ErrNeedsConfirmation, firstWords(st.SQL))
		}
	}

	plan := &Plan{Engine: t.Engine, Statement: st.SQL, Analyzed: analyze}
	query, wantJSON := explainQuery(t.Engine, st.SQL, analyze)

	run := func(conn Conn) error {
		started := time.Now()
		cur, err := conn.Run(ctx, query, nil, true)
		if err != nil {
			return err
		}
		defer cur.Close()
		var cells []Value
		for cur.Next() {
			vals, err := cur.Values()
			if err != nil {
				return err
			}
			cells = append(cells, vals...)
		}
		if err := cur.Err(); err != nil {
			return err
		}
		plan.DurationMS = time.Since(started).Milliseconds()

		if wantJSON {
			// A json column arrives already decoded, as a map or a slice.
			// Rendering that back into text so it can be parsed again produces
			// Go's map syntax, which is not JSON and reads like a core dump.
			if len(cells) == 1 {
				switch cells[0].(type) {
				case map[string]any, []any:
					plan.JSON = cells[0]
					return nil
				}
			}
			var parsed any
			if err := json.Unmarshal([]byte(joinCells(cells)), &parsed); err == nil {
				plan.JSON = parsed
				return nil
			}
			// Fall through to text: a plan we cannot parse is still a plan
			// somebody can read.
		}
		plan.Text = joinCells(cells)
		return nil
	}

	var err error
	if sess != nil {
		if err = sess.begin(); err != nil {
			return nil, err
		}
		defer sess.end()
		err = run(sess.conn)
	} else {
		err = m.Use(ctx, t.Ref, t.User, t.Config, run)
	}
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func explainQuery(engine, sql string, analyze bool) (query string, wantJSON bool) {
	if dialectOf(engine) == MySQL {
		if analyze {
			// MySQL's EXPLAIN ANALYZE is text only, and older servers do not
			// have it at all; the error message says so plainly enough.
			return "EXPLAIN ANALYZE " + sql, false
		}
		return "EXPLAIN FORMAT=JSON " + sql, true
	}
	if analyze {
		return "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + sql, true
	}
	return "EXPLAIN (FORMAT JSON) " + sql, true
}

// NewRunID makes an identifier for one press of Run, which is what a cancel
// names. The client may supply its own so it can cancel a run whose response
// has not arrived yet.
func NewRunID() string { return randomID() }

// joinCells renders a plan that came back as text, one row per line.
func joinCells(cells []Value) string {
	lines := make([]string, 0, len(cells))
	for _, c := range cells {
		lines = append(lines, str(c))
	}
	return strings.Join(lines, "\n")
}
