// Package store owns the SQLite database: opening it with the right pragmas,
// running migrations, and the few queries every other package needs.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"

	_ "modernc.org/sqlite" // pure Go SQLite driver, no CGO
)

// Store is the open database plus the identity of the local server.
type Store struct {
	DB       *sql.DB
	ServerID string
	Hostname string
}

// Open opens (or creates) the database at path, applies migrations, and
// makes sure the local server row exists.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "synchronous(NORMAL)")
	dsn := "file:" + path + "?" + q.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One writer at a time keeps SQLite happy; readers still share the pool.
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: db}
	if err := s.ensureLocalServer(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) ensureLocalServer(ctx context.Context) error {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "islet"
	}
	err := s.DB.QueryRowContext(ctx, `SELECT id, hostname FROM servers WHERE is_local = 1`).Scan(&s.ServerID, &s.Hostname)
	switch {
	case err == nil:
		if s.Hostname != hostname {
			_, _ = s.DB.ExecContext(ctx, `UPDATE servers SET hostname = ? WHERE id = ?`, hostname, s.ServerID)
			s.Hostname = hostname
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		id, err := newID()
		if err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO servers (id, name, hostname, is_local) VALUES (?, ?, ?, 1)`, id, hostname, hostname); err != nil {
			return fmt.Errorf("create local server: %w", err)
		}
		s.ServerID, s.Hostname = id, hostname
		return nil
	default:
		return fmt.Errorf("read local server: %w", err)
	}
}

// Audit records an action in the audit log for the local server.
func (s *Store) Audit(ctx context.Context, actor, action, target, detail string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO audit_log (server_id, actor, action, target, detail) VALUES (?, ?, ?, ?, ?)`,
		s.ServerID, actor, action, target, detail)
	return err
}

// Setting reads a setting; ok is false when it does not exist.
func (s *Store) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetSetting writes a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`, key, value)
	return err
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
