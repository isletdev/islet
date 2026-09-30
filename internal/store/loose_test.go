package store

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// A database with a parent, a child and rows in both — which is the only shape
// where any of this matters. On an empty database every rebuild works, which is
// exactly how a broken one ships.
func peopleAndPets(t *testing.T) *sql.DB {
	t.Helper()
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(ON)")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "t.db")+"?"+q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, s := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE people (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('staff')))`,
		`CREATE TABLE pets (id TEXT PRIMARY KEY, owner TEXT NOT NULL REFERENCES people (id))`,
		`INSERT INTO people (id, kind) VALUES ('p1', 'staff')`,
		`INSERT INTO pets (id, owner) VALUES ('c1', 'p1')`,
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// The reason this mechanism exists: dropping a table other rows point at is
// refused while foreign keys are on, and the pragma that turns them off does
// nothing inside a transaction — which every ordinary migration runs in.
func TestATableOtherRowsPointAtCanBeRebuilt(t *testing.T) {
	db := peopleAndPets(t)
	ctx := context.Background()

	ordinary := migration{version: 1, name: "widen", sql: `
		CREATE TABLE people_new (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('staff','guest')));
		INSERT INTO people_new SELECT id, kind FROM people;
		DROP TABLE people;
		ALTER TABLE people_new RENAME TO people;`}

	// First, prove the problem is real: the same SQL inside a transaction fails.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, ordinary.sql); err == nil {
		if err := tx.Commit(); err == nil {
			t.Fatal("a parent with children was dropped inside a transaction, so this mechanism is no longer needed")
		}
	}
	_ = tx.Rollback()

	// And now the loose path, which is the same SQL with the constraints off.
	loose := ordinary
	loose.sql = "-- islet:no-transaction\n" + ordinary.sql
	if !looseMigration(loose.sql) {
		t.Fatal("the marker on the first line was not recognised")
	}
	if err := applyLoose(ctx, db, loose); err != nil {
		t.Fatalf("the rebuild failed: %v", err)
	}

	var kinds string
	if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name = 'people'`).Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kinds, "guest") {
		t.Error("the table was not widened")
	}
	var owned int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pets p JOIN people o ON o.id = p.owner`).Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if owned != 1 {
		t.Errorf("%d children still point at a parent, want 1", owned)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Errorf("the migration was not recorded: version %d", version)
	}
}

// Constraints off means a mistake is silent, so the mechanism checks afterwards
// and says what is dangling instead of leaving it to be found by a query
// somewhere else months later.
func TestALooseMigrationThatOrphansRowsSaysSo(t *testing.T) {
	db := peopleAndPets(t)
	bad := migration{version: 2, name: "oops", sql: "-- islet:no-transaction\nDROP TABLE people;"}
	err := applyLoose(context.Background(), db, bad)
	if err == nil {
		t.Fatal("a migration that left every pet without an owner was accepted")
	}
	if !strings.Contains(err.Error(), "pointing at nothing") || !strings.Contains(err.Error(), "pets") {
		t.Errorf("the failure does not say what is dangling: %v", err)
	}
}

// Foreign keys go back on for the connection that had them off, because that
// connection returns to the pool and everything after it would otherwise be
// running without constraints.
func TestForeignKeysComeBackOnAfterwards(t *testing.T) {
	db := peopleAndPets(t)
	ctx := context.Background()
	ok := migration{version: 3, name: "noop", sql: "-- islet:no-transaction\nSELECT 1;"}
	if err := applyLoose(ctx, db, ok); err != nil {
		t.Fatal(err)
	}
	// Every connection in the pool must refuse an orphan. One query cannot
	// prove which connection it lands on, so ask several times.
	for i := 0; i < 8; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO pets (id, owner) VALUES ('x', 'nobody')`); err == nil {
			t.Fatal("a pet was given an owner who does not exist: constraints are still off")
		}
	}
}

func TestOnlyTheFirstLineTurnsAMigrationLoose(t *testing.T) {
	if looseMigration("CREATE TABLE t (a);\n-- islet:no-transaction\n") {
		t.Error("the marker was honoured from the middle of a file")
	}
	if !looseMigration("-- islet:no-transaction\nDROP TABLE t;") {
		t.Error("the marker on the first line was not honoured")
	}
	if looseMigration("-- an ordinary migration\nCREATE TABLE t (a);") {
		t.Error("an ordinary migration was treated as loose")
	}
}
