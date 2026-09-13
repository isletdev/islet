package sqlclient

import (
	"reflect"
	"strings"
	"testing"
)

func TestBindNamedPostgres(t *testing.T) {
	sql, args, err := bindNamed(
		"SELECT * FROM users WHERE org = :org AND (id = :id OR parent = :id)",
		Postgres, map[string]any{"org": "acme", "id": 7})
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM users WHERE org = $1 AND (id = $2 OR parent = $2)"
	if sql != want {
		t.Errorf("sql = %q\nwant %q", sql, want)
	}
	if !reflect.DeepEqual(args, []any{"acme", 7}) {
		t.Errorf("args = %#v, want one entry per distinct name", args)
	}
}

func TestBindNamedMySQL(t *testing.T) {
	sql, args, err := bindNamed(
		"SELECT * FROM users WHERE id = :id OR parent = :id",
		MySQL, map[string]any{"id": 7})
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM users WHERE id = ? OR parent = ?"
	if sql != want {
		t.Errorf("sql = %q\nwant %q", sql, want)
	}
	// MySQL cannot refer to a placeholder twice, so the value goes twice.
	if !reflect.DeepEqual(args, []any{7, 7}) {
		t.Errorf("args = %#v, want the value once per occurrence", args)
	}
}

func TestBindNamedLeavesTextAlone(t *testing.T) {
	for _, sql := range []string{
		"SELECT ':id' AS literal",    // inside a string
		"SELECT x::int FROM t",       // a cast
		"SELECT * FROM t WHERE a=$1", // already positional
	} {
		got, args, err := bindNamed(sql, Postgres, nil)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		if got != sql || args != nil {
			t.Errorf("bindNamed(%q) rewrote it to %q with %#v", sql, got, args)
		}
	}
}

func TestBindNamedRefusesAMissingValue(t *testing.T) {
	_, _, err := bindNamed("SELECT :a, :b, :a", Postgres, map[string]any{"a": 1})
	if err == nil {
		t.Fatal("a missing parameter should be an error, not an empty string in the query")
	}
	if !strings.Contains(err.Error(), ":b") {
		t.Errorf("the error should name the missing parameter, got %q", err)
	}
	if strings.Count(err.Error(), ":a") != 0 {
		t.Errorf("the error named a parameter that was supplied: %q", err)
	}
}

// TestBindNamedNeverInterpolates is the injection test. Whatever the value
// looks like, it must not appear in the statement text.
func TestBindNamedNeverInterpolates(t *testing.T) {
	nasty := "'; DROP TABLE users; --"
	sql, args, err := bindNamed("SELECT * FROM t WHERE name = :name", Postgres, map[string]any{"name": nasty})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "DROP") {
		t.Fatalf("the value reached the statement text: %q", sql)
	}
	if len(args) != 1 || args[0] != nasty {
		t.Fatalf("the value should travel as an argument, got %#v", args)
	}
}
