package sqlclient

import (
	"context"
	"sort"
	"time"
)

func init() { RegisterIntrospector("postgres", pgIntrospector{}) }

type pgIntrospector struct{}

// The catalog is read rather than information_schema wherever there is a
// choice: it is faster, it carries the row estimate, and it does not hide
// objects the connected user cannot see behind a permission check that makes
// a tree silently incomplete.

const pgTablesSQL = `
SELECT n.nspname,
       c.relname,
       c.relkind::text AS relkind,
       COALESCE(c.reltuples, -1)::bigint AS rows,
       COALESCE(obj_description(c.oid, 'pg_class'), '') AS comment
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r','p','v','m','f')
  AND n.nspname NOT IN ('pg_catalog','information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp%'
ORDER BY n.nspname, c.relname`

const pgColumnsSQL = `
SELECT n.nspname,
       c.relname,
       a.attname,
       format_type(a.atttypid, a.atttypmod) AS type,
       t.typname                            AS base_type,
       NOT a.attnotnull                     AS nullable,
       COALESCE(pg_get_expr(d.adbin, d.adrelid), '') AS default_expr,
       a.attidentity <> ''                  AS identity,
       a.attnum,
       COALESCE(col_description(c.oid, a.attnum), '') AS comment,
       COALESCE(pk.is_pk, false)            AS primary_key
FROM pg_attribute a
JOIN pg_class c        ON c.oid = a.attrelid
JOIN pg_namespace n    ON n.oid = c.relnamespace
JOIN pg_type t         ON t.oid = a.atttypid
LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
LEFT JOIN LATERAL (
  SELECT true AS is_pk
  FROM pg_constraint con
  WHERE con.conrelid = c.oid AND con.contype = 'p' AND a.attnum = ANY (con.conkey)
) pk ON true
WHERE a.attnum > 0
  AND NOT a.attisdropped
  AND c.relkind IN ('r','p','v','m','f')
  AND n.nspname NOT IN ('pg_catalog','information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp%'
ORDER BY n.nspname, c.relname, a.attnum`

// Both directions in one pass. The reverse direction is what makes "what
// points at this row" possible, and it costs nothing extra to collect here.
const pgForeignKeysSQL = `
SELECT con.conname,
       ns.nspname   AS schema,
       cl.relname   AS table_name,
       fns.nspname  AS ref_schema,
       fcl.relname  AS ref_table,
       (SELECT array_agg(att.attname ORDER BY u.ord)
          FROM unnest(con.conkey) WITH ORDINALITY AS u(attnum, ord)
          JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = u.attnum) AS cols,
       (SELECT array_agg(att.attname ORDER BY u.ord)
          FROM unnest(con.confkey) WITH ORDINALITY AS u(attnum, ord)
          JOIN pg_attribute att ON att.attrelid = con.confrelid AND att.attnum = u.attnum) AS ref_cols,
       con.confdeltype,
       con.confupdtype
FROM pg_constraint con
JOIN pg_class cl      ON cl.oid  = con.conrelid
JOIN pg_namespace ns  ON ns.oid  = cl.relnamespace
JOIN pg_class fcl     ON fcl.oid = con.confrelid
JOIN pg_namespace fns ON fns.oid = fcl.relnamespace
WHERE con.contype = 'f'
  AND ns.nspname NOT IN ('pg_catalog','information_schema')
ORDER BY ns.nspname, cl.relname, con.conname`

const pgStatStatementsSQL = `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`

func (pgIntrospector) Tree(ctx context.Context, conn Conn) (*Tree, error) {
	tree := &Tree{Engine: "postgres", FetchedAt: time.Now()}

	_, rows, partial, err := queryAll(ctx, conn, pgTablesSQL)
	if err != nil {
		return nil, err
	}
	tree.Partial = partial

	// schema name -> table name -> *Table, so the three passes can find each
	// other without three nested loops.
	index := map[string]map[string]*Table{}
	order := []string{}
	for _, r := range rows {
		schema, name := str(r[0]), str(r[1])
		if _, ok := index[schema]; !ok {
			index[schema] = map[string]*Table{}
			order = append(order, schema)
		}
		index[schema][name] = &Table{
			Schema: schema, Name: name,
			Kind:    pgRelKind(str(r[2])),
			Rows:    i64(r[3]),
			Comment: str(r[4]),
		}
	}

	_, cols, partialCols, err := queryAll(ctx, conn, pgColumnsSQL)
	if err != nil {
		return nil, err
	}
	tree.Partial = tree.Partial || partialCols
	for _, r := range cols {
		tb := lookup(index, str(r[0]), str(r[1]))
		if tb == nil {
			continue
		}
		typeName := str(r[3])
		tb.Columns = append(tb.Columns, ColumnInfo{
			Name:       str(r[2]),
			Type:       typeName,
			Class:      postgresClass(str(r[4])),
			Nullable:   boolean(r[5]),
			Default:    str(r[6]),
			Identity:   boolean(r[7]),
			Position:   int(i64(r[8])),
			Comment:    str(r[9]),
			PrimaryKey: boolean(r[10]),
		})
	}

	_, fks, _, err := queryAll(ctx, conn, pgForeignKeysSQL)
	if err != nil {
		return nil, err
	}
	for _, r := range fks {
		fk := ForeignKey{
			Name:       str(r[0]),
			Schema:     str(r[1]),
			Table:      str(r[2]),
			RefSchema:  str(r[3]),
			RefTable:   str(r[4]),
			Columns:    strs(r[5]),
			RefColumns: strs(r[6]),
			OnDelete:   pgAction(str(r[7])),
			OnUpdate:   pgAction(str(r[8])),
		}
		if tb := lookup(index, fk.Schema, fk.Table); tb != nil {
			tb.ForeignKeys = append(tb.ForeignKeys, fk)
		}
		if tb := lookup(index, fk.RefSchema, fk.RefTable); tb != nil {
			tb.ReferencedBy = append(tb.ReferencedBy, fk)
		}
	}

	if _, ext, _, err := queryAll(ctx, conn, pgStatStatementsSQL); err == nil && len(ext) == 1 {
		tree.StatStatements = boolean(ext[0][0])
	}

	tree.Schemas = assemble(index, order)
	return tree, nil
}

const pgIndexesSQL = `
SELECT i.relname,
       ix.indisunique,
       ix.indisprimary,
       am.amname,
       (SELECT array_agg(a.attname ORDER BY k.ord)
          FROM unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord)
          JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = k.attnum) AS cols
FROM pg_index ix
JOIN pg_class i      ON i.oid = ix.indexrelid
JOIN pg_class c      ON c.oid = ix.indrelid
JOIN pg_namespace n  ON n.oid = c.relnamespace
JOIN pg_am am        ON am.oid = i.relam
WHERE n.nspname = $1 AND c.relname = $2
ORDER BY i.relname`

const pgConstraintsSQL = `
SELECT con.conname, con.contype::text, pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class c     ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2
ORDER BY con.conname`

func (p pgIntrospector) Table(ctx context.Context, conn Conn, schema, table string) (*TableDetail, error) {
	tree, err := p.Tree(ctx, conn)
	if err != nil {
		return nil, err
	}
	base := findTable(tree, schema, table)
	if base == nil {
		return nil, ErrNotFound
	}
	detail := &TableDetail{Table: *base, Indexes: []Index{}, Constraints: []Constraint{}}

	_, rows, _, err := queryAll(ctx, conn, pgIndexesSQL, schema, table)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		detail.Indexes = append(detail.Indexes, Index{
			Name:    str(r[0]),
			Unique:  boolean(r[1]),
			Primary: boolean(r[2]),
			Method:  str(r[3]),
			Columns: strs(r[4]),
		})
	}

	_, rows, _, err = queryAll(ctx, conn, pgConstraintsSQL, schema, table)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		detail.Constraints = append(detail.Constraints, Constraint{
			Name:       str(r[0]),
			Type:       pgConstraintType(str(r[1])),
			Definition: str(r[2]),
		})
	}
	return detail, nil
}

func pgRelKind(k string) string {
	switch k {
	case "v":
		return "view"
	case "m":
		return "matview"
	case "f":
		return "foreign"
	default:
		return "table"
	}
}

func pgConstraintType(t string) string {
	switch t {
	case "p":
		return "primary"
	case "u":
		return "unique"
	case "c":
		return "check"
	case "f":
		return "foreign"
	case "x":
		return "exclusion"
	}
	return t
}

func pgAction(c string) string {
	switch c {
	case "a":
		return "no action"
	case "r":
		return "restrict"
	case "c":
		return "cascade"
	case "n":
		return "set null"
	case "d":
		return "set default"
	}
	return ""
}

func lookup(index map[string]map[string]*Table, schema, table string) *Table {
	if s, ok := index[schema]; ok {
		return s[table]
	}
	return nil
}

func findTable(t *Tree, schema, table string) *Table {
	for i := range t.Schemas {
		if t.Schemas[i].Name != schema {
			continue
		}
		for j := range t.Schemas[i].Tables {
			if t.Schemas[i].Tables[j].Name == table {
				return &t.Schemas[i].Tables[j]
			}
		}
	}
	return nil
}

// assemble turns the working index back into the ordered tree the client draws.
func assemble(index map[string]map[string]*Table, order []string) []Schema {
	sort.Strings(order)
	out := make([]Schema, 0, len(order))
	for _, name := range order {
		tables := make([]Table, 0, len(index[name]))
		for _, tb := range index[name] {
			tables = append(tables, *tb)
		}
		sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })
		out = append(out, Schema{Name: name, Tables: tables})
	}
	return out
}
