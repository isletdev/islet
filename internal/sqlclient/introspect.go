package sqlclient

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The introspection model. It is the least glamorous part of this package and
// everything good depends on it: the tree, the search, the autocomplete, the
// table viewer, foreign-key navigation and, later, inline editing all read
// from here.

// Tree is one connection's whole schema, as much of it as is worth holding.
type Tree struct {
	Engine    string    `json:"engine"`
	Schemas   []Schema  `json:"schemas"`
	FetchedAt time.Time `json:"fetchedAt"`
	// StatStatements is whether pg_stat_statements is installed, which is what
	// makes the slow-query loop possible.
	StatStatements bool `json:"statStatements"`
	// Partial is set when introspection hit its own limits on a very large
	// schema. Saying so beats silently showing three quarters of a database.
	Partial bool   `json:"partial,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Schema is a namespace of tables.
type Schema struct {
	Name   string  `json:"name"`
	Tables []Table `json:"tables"`
}

// Table is one table, view or materialised view.
type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // table | view | matview | foreign
	// Rows is the catalog's estimate, never a COUNT(*): counting a table to
	// draw a tree is how a schema browser becomes the slowest page in a panel.
	Rows    int64        `json:"rows"`
	Comment string       `json:"comment,omitempty"`
	Columns []ColumnInfo `json:"columns,omitempty"`
	// ForeignKeys is what this table points at. ReferencedBy is what points at
	// it, which is the half people forget and the half that makes row
	// navigation feel like the database is joined up.
	ForeignKeys  []ForeignKey `json:"foreignKeys,omitempty"`
	ReferencedBy []ForeignKey `json:"referencedBy,omitempty"`
}

// ColumnInfo is a column as the catalog describes it, which is more than a
// result set knows: nullability, defaults, identity and whether it is a key.
type ColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Class      Class  `json:"class"`
	Nullable   bool   `json:"nullable"`
	Default    string `json:"default,omitempty"`
	Identity   bool   `json:"identity,omitempty"`
	PrimaryKey bool   `json:"primaryKey,omitempty"`
	Position   int    `json:"position"`
	Comment    string `json:"comment,omitempty"`
}

// ForeignKey is one relationship, described from the referencing side
// whichever direction it is being read in.
type ForeignKey struct {
	Name       string   `json:"name"`
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"refSchema"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	OnDelete   string   `json:"onDelete,omitempty"`
	OnUpdate   string   `json:"onUpdate,omitempty"`
}

// Index is one index and the columns it covers.
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	Method  string   `json:"method,omitempty"`
}

// Constraint is a primary key, unique, check or exclusion constraint, with the
// engine's own rendering of it.
type Constraint struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // primary | unique | check | exclusion | foreign
	Definition string `json:"definition"`
}

// TableDetail is everything about one table, for the table viewer.
type TableDetail struct {
	Table
	Indexes     []Index      `json:"indexes"`
	Constraints []Constraint `json:"constraints"`
}

// Introspector reads a schema out of one engine.
type Introspector interface {
	Tree(ctx context.Context, conn Conn) (*Tree, error)
	Table(ctx context.Context, conn Conn, schema, table string) (*TableDetail, error)
}

var introspectors = map[string]Introspector{}

// RegisterIntrospector makes an introspector available for an engine name.
func RegisterIntrospector(engine string, in Introspector) { introspectors[engine] = in }

func introspectorFor(engine string) (Introspector, error) {
	if in, ok := introspectors[engine]; ok {
		return in, nil
	}
	if dialectOf(engine) == MySQL {
		if in, ok := introspectors["mysql"]; ok {
			return in, nil
		}
	}
	return nil, fmt.Errorf("no schema reader for %q", engine)
}

// introspectRowLimit is the ceiling on rows read from the catalog in one pass.
// A schema large enough to hit it is a schema the tree could not usefully draw
// anyway, and Partial says so rather than truncating in silence.
const introspectRowLimit = 200_000

// SchemaCache holds one tree per connection for SchemaCacheTTL, with an
// explicit refresh and an invalidation after DDL.
type SchemaCache struct {
	now func() time.Time

	mu      sync.Mutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	tree *Tree
	at   time.Time
}

// NewSchemaCache builds a cache. now is injected for the tests.
func NewSchemaCache(now func() time.Time) *SchemaCache {
	if now == nil {
		now = time.Now
	}
	return &SchemaCache{now: now, entries: map[string]*cacheEntry{}}
}

// Get returns the cached tree for ref, loading it if it is missing or stale.
// force skips the cache, which is what the refresh control does.
func (c *SchemaCache) Get(ctx context.Context, ref, engine string, force bool, load func(context.Context) (*Tree, error)) (*Tree, error) {
	if !force {
		c.mu.Lock()
		e, ok := c.entries[ref]
		c.mu.Unlock()
		if ok && c.now().Sub(e.at) < SchemaCacheTTL {
			return e.tree, nil
		}
	}
	tree, err := load(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[ref] = &cacheEntry{tree: tree, at: c.now()}
	c.mu.Unlock()
	return tree, nil
}

// Invalidate drops a connection's cached tree. Called after any statement the
// classifier called DDL, and when a connection is edited or forgotten.
func (c *SchemaCache) Invalidate(ref string) {
	c.mu.Lock()
	delete(c.entries, ref)
	c.mu.Unlock()
}

// Find is the schema search: a flat, ranked list over every schema, table,
// view and column in the tree. The tree gets long, and scrolling it is not a
// search.
type Match struct {
	Kind   string `json:"kind"` // schema | table | view | column
	Schema string `json:"schema"`
	Table  string `json:"table,omitempty"`
	Column string `json:"column,omitempty"`
	Type   string `json:"type,omitempty"`
	Label  string `json:"label"`
	score  int
}

// Find returns the best matches for a query, tables before columns and a
// prefix before a substring, which is the order people expect without being
// able to say so.
func (t *Tree) Find(query string, limit int) []Match {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []Match
	add := func(m Match, hay string, base int) {
		i := strings.Index(strings.ToLower(hay), q)
		if i < 0 {
			return
		}
		m.score = base - i*2 - len(hay)/8
		if i == 0 {
			m.score += 40
		}
		if strings.EqualFold(hay, q) {
			m.score += 60
		}
		out = append(out, m)
	}
	for _, s := range t.Schemas {
		add(Match{Kind: "schema", Schema: s.Name, Label: s.Name}, s.Name, 100)
		for _, tb := range s.Tables {
			kind := "table"
			if tb.Kind != "table" {
				kind = tb.Kind
			}
			add(Match{Kind: kind, Schema: s.Name, Table: tb.Name, Label: s.Name + "." + tb.Name}, tb.Name, 200)
			for _, col := range tb.Columns {
				add(Match{
					Kind: "column", Schema: s.Name, Table: tb.Name, Column: col.Name,
					Type: col.Type, Label: s.Name + "." + tb.Name + "." + col.Name,
				}, col.Name, 150)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// queryAll runs a catalog query and reads it into memory. It is only ever used
// for the catalog, which is why an unbounded read would be a bug and the limit
// is not a parameter callers can raise.
func queryAll(ctx context.Context, conn Conn, sql string, args ...any) ([]Column, [][]Value, bool, error) {
	cur, err := conn.Run(ctx, sql, args, true)
	if err != nil {
		return nil, nil, false, err
	}
	defer cur.Close()
	cols := cur.Columns()
	var rows [][]Value
	for len(rows) < introspectRowLimit && cur.Next() {
		v, err := cur.Values()
		if err != nil {
			return cols, rows, false, err
		}
		rows = append(rows, v)
	}
	partial := len(rows) == introspectRowLimit && cur.Next()
	return cols, rows, partial, cur.Err()
}

// The accessors below are deliberately forgiving. Values arrive already
// encoded for the wire, so a bigint is a string and a smallint is a number,
// and the catalog queries should not have to care which.

func str(v Value) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func i64(v Value) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			f, err := strconv.ParseFloat(x, 64)
			if err != nil {
				return -1
			}
			return int64(f)
		}
		return n
	}
	return -1
}

func boolean(v Value) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(x) {
		case "t", "true", "yes", "1":
			return true
		}
	case int64:
		return x != 0
	case float64:
		return x != 0
	}
	return false
}

func strs(v Value) []string {
	arr, ok := v.([]Value)
	if !ok {
		if s := str(v); s != "" {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, str(e))
	}
	return out
}
