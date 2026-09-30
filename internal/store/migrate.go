package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded migration files, sorted by version.
// File names are NNNN_name.sql.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, rest, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: name must be NNNN_name.sql", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version prefix", name)
		}
		body, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: strings.TrimSuffix(rest, ".sql"), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", out[i].version)
		}
	}
	return out, nil
}

// migrate applies every migration newer than the current schema version,
// each inside its own transaction.
// newest is the highest migration this binary carries. A database that has
// been further than this was written by a newer Islet, and running against a
// schema we do not know is how a rollback corrupts data rather than recovering
// from it.
func newest(ms []migration) int {
	high := 0
	for _, m := range ms {
		if m.version > high {
			high = m.version
		}
	}
	return high
}

func migrate(ctx context.Context, db *sql.DB) (applied int, err error) {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	migs, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if high := newest(migs); current > high {
		return 0, fmt.Errorf("this database is at schema version %d but this build only knows %d: it was written by a newer Islet. Install that version again, or restore a backup", current, high)
	}
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		// A migration that rebuilds a table other tables point at cannot run
		// inside a transaction, because the only way to drop a foreign-key
		// parent that has live children is to turn foreign keys off — and
		// `PRAGMA foreign_keys` is a no-op inside one. Such a migration says so
		// on its first line.
		if looseMigration(m.sql) {
			if err := applyLoose(ctx, db, m); err != nil {
				return applied, err
			}
			applied++
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			_ = tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// looseMigration reports whether a migration asked to run outside the usual
// transaction. The marker is on the first line so that reading the file says so
// before anything else about it.
func looseMigration(sql string) bool {
	first, _, _ := strings.Cut(sql, "\n")
	return strings.Contains(first, "islet:no-transaction")
}

// applyLoose runs one migration on a connection of its own with foreign keys
// switched off, and will not record it unless the database is still whole.
//
// The transaction is still there — the statements are atomic — but it is opened
// after the pragma rather than around it, which is the whole difference. The
// connection is dedicated so that nothing else runs while constraints are down,
// and the pragma is put back on every path out, because that connection returns
// to the pool afterwards and must not take a disabled constraint with it.
//
// The check happens *inside* the transaction, before anything is committed. It
// used to run after — which meant a broken database failed to start exactly
// once: the migration was already recorded, so the next start skipped it and
// came up clean on a database the daemon had just announced as broken. With
// Restart=always in the unit, that is a two-second blip in the journal and then
// silence. Found by a release check that planted a dangling row and started the
// daemon twice.
func applyLoose(ctx context.Context, db *sql.DB, m migration) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`) }()

	// What was already broken before this migration touched anything. Without
	// it, a violation that has been in the database for a year is reported as
	// something this migration did, and the person reading it goes looking in
	// the wrong place.
	before, err := danglingRows(ctx, conn)
	if err != nil {
		return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
	}

	// With constraints off a mistake is silent: rows pointing at a table that is
	// no longer there, and nothing said until somebody reads them. So it is
	// checked here, where a failure still rolls the whole thing back.
	after, err := danglingRows(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
	}
	if len(after) > len(before) {
		_ = tx.Rollback()
		return fmt.Errorf("migration %04d_%s would leave rows pointing at nothing: %s",
			m.version, m.name, strings.Join(after, ", "))
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// danglingRows is what `PRAGMA foreign_key_check` has to say, as a short list.
func danglingRows(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]string, error) {
	rows, err := q.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var table, parent string
		var rowid, fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return nil, err
		}
		if len(out) < 6 {
			out = append(out, table+" -> "+parent)
		} else {
			out = append(out, "…")
			break
		}
	}
	return out, rows.Err()
}
