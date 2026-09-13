package sqlclient

import (
	"context"
	"strings"
	"time"
)

func init() { RegisterIntrospector("mysql", myIntrospector{}) }

type myIntrospector struct{}

// MySQL has no catalog to read directly, so this is information_schema, which
// is slower and which reports TABLE_ROWS as an estimate for InnoDB and as a
// count for a few other engines. Either way it is not a COUNT(*), which is the
// property that matters.

const mySystemSchemas = `('mysql','information_schema','performance_schema','sys')`

const myTablesSQL = `
SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE,
       COALESCE(TABLE_ROWS, -1) AS row_estimate,
       COALESCE(TABLE_COMMENT, '') AS table_comment
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ` + mySystemSchemas + `
ORDER BY TABLE_SCHEMA, TABLE_NAME`

const myColumnsSQL = `
SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME,
       COLUMN_TYPE, DATA_TYPE,
       IS_NULLABLE, COALESCE(COLUMN_DEFAULT, ''), COALESCE(EXTRA, ''),
       ORDINAL_POSITION, COALESCE(COLUMN_COMMENT, ''), COLUMN_KEY
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA NOT IN ` + mySystemSchemas + `
ORDER BY TABLE_SCHEMA, TABLE_NAME, ORDINAL_POSITION`

// One row per column of a composite key, so this is folded back together
// below. Both directions come out of the same query.
const myForeignKeysSQL = `
SELECT k.CONSTRAINT_NAME, k.TABLE_SCHEMA, k.TABLE_NAME, k.COLUMN_NAME,
       k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
       k.ORDINAL_POSITION,
       COALESCE(r.DELETE_RULE, ''), COALESCE(r.UPDATE_RULE, '')
FROM information_schema.KEY_COLUMN_USAGE k
LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
       ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
      AND r.CONSTRAINT_NAME   = k.CONSTRAINT_NAME
      AND r.TABLE_NAME        = k.TABLE_NAME
WHERE k.REFERENCED_TABLE_NAME IS NOT NULL
  AND k.TABLE_SCHEMA NOT IN ` + mySystemSchemas + `
ORDER BY k.TABLE_SCHEMA, k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`

func (myIntrospector) Tree(ctx context.Context, conn Conn) (*Tree, error) {
	tree := &Tree{Engine: "mysql", FetchedAt: time.Now()}

	_, rows, partial, err := queryAll(ctx, conn, myTablesSQL)
	if err != nil {
		return nil, err
	}
	tree.Partial = partial

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
			Kind:    myTableKind(str(r[2])),
			Rows:    i64(r[3]),
			Comment: str(r[4]),
		}
	}

	_, cols, partialCols, err := queryAll(ctx, conn, myColumnsSQL)
	if err != nil {
		return nil, err
	}
	tree.Partial = tree.Partial || partialCols
	for _, r := range cols {
		tb := lookup(index, str(r[0]), str(r[1]))
		if tb == nil {
			continue
		}
		extra := strings.ToLower(str(r[7]))
		tb.Columns = append(tb.Columns, ColumnInfo{
			Name:  str(r[2]),
			Type:  str(r[3]), // COLUMN_TYPE keeps the width: varchar(64), not varchar
			Class: mysqlClass(str(r[4])),
			// IS_NULLABLE is the string YES or NO, which is information_schema
			// all over.
			Nullable:   strings.EqualFold(str(r[5]), "YES"),
			Default:    str(r[6]),
			Identity:   strings.Contains(extra, "auto_increment"),
			Position:   int(i64(r[8])),
			Comment:    str(r[9]),
			PrimaryKey: strings.EqualFold(str(r[10]), "PRI"),
		})
	}

	_, fks, _, err := queryAll(ctx, conn, myForeignKeysSQL)
	if err != nil {
		return nil, err
	}
	for _, fk := range foldMySQLForeignKeys(fks) {
		if tb := lookup(index, fk.Schema, fk.Table); tb != nil {
			tb.ForeignKeys = append(tb.ForeignKeys, fk)
		}
		if tb := lookup(index, fk.RefSchema, fk.RefTable); tb != nil {
			tb.ReferencedBy = append(tb.ReferencedBy, fk)
		}
	}

	tree.Schemas = assemble(index, order)
	return tree, nil
}

// foldMySQLForeignKeys turns one row per key column back into one entry per
// constraint, keeping the column order the ordinal position gave.
func foldMySQLForeignKeys(rows [][]Value) []ForeignKey {
	type key struct{ schema, table, name string }
	var order []key
	byKey := map[key]*ForeignKey{}
	for _, r := range rows {
		k := key{str(r[1]), str(r[2]), str(r[0])}
		fk, ok := byKey[k]
		if !ok {
			fk = &ForeignKey{
				Name:      k.name,
				Schema:    k.schema,
				Table:     k.table,
				RefSchema: str(r[4]),
				RefTable:  str(r[5]),
				OnDelete:  strings.ToLower(str(r[8])),
				OnUpdate:  strings.ToLower(str(r[9])),
			}
			byKey[k] = fk
			order = append(order, k)
		}
		fk.Columns = append(fk.Columns, str(r[3]))
		fk.RefColumns = append(fk.RefColumns, str(r[6]))
	}
	out := make([]ForeignKey, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

const myIndexesSQL = `
SELECT INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME, INDEX_TYPE
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY INDEX_NAME, SEQ_IN_INDEX`

const myConstraintsSQL = `
SELECT CONSTRAINT_NAME, CONSTRAINT_TYPE
FROM information_schema.TABLE_CONSTRAINTS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY CONSTRAINT_NAME`

func (m myIntrospector) Table(ctx context.Context, conn Conn, schema, table string) (*TableDetail, error) {
	tree, err := m.Tree(ctx, conn)
	if err != nil {
		return nil, err
	}
	base := findTable(tree, schema, table)
	if base == nil {
		return nil, ErrNotFound
	}
	detail := &TableDetail{Table: *base, Indexes: []Index{}, Constraints: []Constraint{}}

	_, rows, _, err := queryAll(ctx, conn, myIndexesSQL, schema, table)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Index{}
	var order []string
	for _, r := range rows {
		name := str(r[0])
		ix, ok := byName[name]
		if !ok {
			ix = &Index{
				Name:    name,
				Unique:  i64(r[1]) == 0, // NON_UNIQUE, so the sense is inverted
				Primary: name == "PRIMARY",
				Method:  strings.ToLower(str(r[4])),
			}
			byName[name] = ix
			order = append(order, name)
		}
		ix.Columns = append(ix.Columns, str(r[3]))
	}
	for _, name := range order {
		detail.Indexes = append(detail.Indexes, *byName[name])
	}

	_, rows, _, err = queryAll(ctx, conn, myConstraintsSQL, schema, table)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		detail.Constraints = append(detail.Constraints, Constraint{
			Name: str(r[0]),
			Type: strings.ToLower(strings.ReplaceAll(str(r[1]), " KEY", "")),
		})
	}
	return detail, nil
}

func myTableKind(t string) string {
	switch strings.ToUpper(t) {
	case "VIEW":
		return "view"
	case "SYSTEM VIEW":
		return "view"
	default:
		return "table"
	}
}
