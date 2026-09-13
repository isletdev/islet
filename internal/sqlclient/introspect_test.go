package sqlclient

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// A tiny two-table schema: orders.customer_id -> customers.id.
func pgCatalogFixture() *fakeDriver {
	return newFakeDriver().
		on(pgTablesSQL, fakeResult{
			cols: []Column{{Name: "nspname"}, {Name: "relname"}, {Name: "relkind"}, {Name: "rows"}, {Name: "comment"}},
			rows: [][]Value{
				{"public", "customers", "r", "12", ""},
				{"public", "orders", "r", "340", "one row per order"},
				{"public", "order_summary", "v", "-1", ""},
			},
		}).
		on(pgColumnsSQL, fakeResult{
			cols: []Column{{Name: "n"}},
			rows: [][]Value{
				{"public", "customers", "id", "integer", "int4", false, "", true, int64(1), "", true},
				{"public", "customers", "email", "text", "text", true, "", false, int64(2), "", false},
				{"public", "orders", "id", "bigint", "int8", false, "", true, int64(1), "", true},
				{"public", "orders", "customer_id", "integer", "int4", true, "", false, int64(2), "", false},
			},
		}).
		on(pgForeignKeysSQL, fakeResult{
			cols: []Column{{Name: "conname"}},
			rows: [][]Value{
				{"orders_customer_id_fkey", "public", "orders", "public", "customers",
					[]Value{"customer_id"}, []Value{"id"}, "c", "a"},
			},
		}).
		on(pgStatStatementsSQL, fakeResult{
			cols: []Column{{Name: "exists"}},
			rows: [][]Value{{true}},
		})
}

func TestPostgresTreeReadsBothDirectionsOfAForeignKey(t *testing.T) {
	d := pgCatalogFixture()
	conn, err := d.Open(context.Background(), Config{Engine: "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := pgIntrospector{}.Tree(context.Background(), conn)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}

	if len(tree.Schemas) != 1 || tree.Schemas[0].Name != "public" {
		t.Fatalf("schemas = %+v, want one called public", tree.Schemas)
	}
	tables := tree.Schemas[0].Tables
	if len(tables) != 3 {
		t.Fatalf("got %d tables, want 3", len(tables))
	}

	orders := findTable(tree, "public", "orders")
	customers := findTable(tree, "public", "customers")
	if orders == nil || customers == nil {
		t.Fatal("orders and customers should both be in the tree")
	}
	if orders.Rows != 340 {
		t.Errorf("row estimate = %d, want 340 from the catalog and not a COUNT(*)", orders.Rows)
	}
	if len(orders.ForeignKeys) != 1 {
		t.Fatalf("orders should point at one table, got %+v", orders.ForeignKeys)
	}
	fk := orders.ForeignKeys[0]
	if fk.RefTable != "customers" || !reflect.DeepEqual(fk.Columns, []string{"customer_id"}) ||
		!reflect.DeepEqual(fk.RefColumns, []string{"id"}) {
		t.Errorf("outgoing key = %+v", fk)
	}
	if fk.OnDelete != "cascade" {
		t.Errorf("on delete = %q, want cascade", fk.OnDelete)
	}

	// The half people forget.
	if len(customers.ReferencedBy) != 1 || customers.ReferencedBy[0].Table != "orders" {
		t.Fatalf("customers should know that orders points at it, got %+v", customers.ReferencedBy)
	}

	// A view is not a table, and the tree says which is which.
	if v := findTable(tree, "public", "order_summary"); v == nil || v.Kind != "view" {
		t.Errorf("order_summary should be a view, got %+v", v)
	}
	if !tree.StatStatements {
		t.Error("pg_stat_statements was reported as installed and should be recorded: the slow-query loop needs it")
	}
	if id := customers.Columns[0]; !id.PrimaryKey || id.Nullable {
		t.Errorf("customers.id = %+v, want a non-null primary key", id)
	}
	if orders.Columns[0].Class != ClassDecimal {
		t.Errorf("a bigint column should be classed decimal so it survives JSON, got %s", orders.Columns[0].Class)
	}
}

func TestMySQLCompositeForeignKeyIsFoldedBackTogether(t *testing.T) {
	rows := [][]Value{
		{"fk_line", "shop", "order_lines", "order_id", "shop", "orders", "id", int64(1), "CASCADE", "NO ACTION"},
		{"fk_line", "shop", "order_lines", "org_id", "shop", "orders", "org_id", int64(2), "CASCADE", "NO ACTION"},
		{"fk_other", "shop", "order_lines", "sku", "shop", "products", "sku", int64(1), "RESTRICT", "NO ACTION"},
	}
	got := foldMySQLForeignKeys(rows)
	if len(got) != 2 {
		t.Fatalf("got %d keys, want 2", len(got))
	}
	if !reflect.DeepEqual(got[0].Columns, []string{"order_id", "org_id"}) {
		t.Errorf("composite columns = %v, want them in ordinal order", got[0].Columns)
	}
	if !reflect.DeepEqual(got[0].RefColumns, []string{"id", "org_id"}) {
		t.Errorf("composite referenced columns = %v", got[0].RefColumns)
	}
	if got[0].OnDelete != "cascade" {
		t.Errorf("on delete = %q", got[0].OnDelete)
	}
}

func TestSchemaSearchRanksTablesAndPrefixesFirst(t *testing.T) {
	tree := &Tree{Schemas: []Schema{{Name: "public", Tables: []Table{
		{Schema: "public", Name: "users", Kind: "table", Columns: []ColumnInfo{{Name: "id"}, {Name: "user_agent"}}},
		{Schema: "public", Name: "audit_users", Kind: "table"},
	}}}}

	got := tree.Find("users", 10)
	if len(got) == 0 {
		t.Fatal("no matches")
	}
	if got[0].Kind != "table" || got[0].Table != "users" {
		t.Errorf("first match = %+v, want the table whose name starts with the query", got[0])
	}
	labels := make([]string, len(got))
	for i, m := range got {
		labels[i] = m.Label
	}
	if len(got) < 2 {
		t.Errorf("audit_users should match too, got %v", labels)
	}
	if tree.Find("", 10) != nil {
		t.Error("an empty query should match nothing rather than everything")
	}
}

func TestSchemaCacheHoldsAndInvalidates(t *testing.T) {
	clk := newClock()
	c := NewSchemaCache(clk.now)
	loads := 0
	load := func(context.Context) (*Tree, error) {
		loads++
		return &Tree{Engine: "postgres"}, nil
	}

	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), "db1", "postgres", false, load); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 1 {
		t.Fatalf("loaded %d times inside the cache window, want 1", loads)
	}

	// An explicit refresh always reloads: that is what the refresh control is.
	if _, err := c.Get(context.Background(), "db1", "postgres", true, load); err != nil {
		t.Fatal(err)
	}
	if loads != 2 {
		t.Fatalf("a forced refresh should reload, loads = %d", loads)
	}

	clk.advance(SchemaCacheTTL + time.Second)
	if _, err := c.Get(context.Background(), "db1", "postgres", false, load); err != nil {
		t.Fatal(err)
	}
	if loads != 3 {
		t.Fatalf("a stale entry should reload, loads = %d", loads)
	}

	// DDL drops it, which is why the classifier reports DDL at all.
	c.Invalidate("db1")
	if _, err := c.Get(context.Background(), "db1", "postgres", false, load); err != nil {
		t.Fatal(err)
	}
	if loads != 4 {
		t.Fatalf("after invalidation the tree should reload, loads = %d", loads)
	}
}
