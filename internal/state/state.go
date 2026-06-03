package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func Open(home string) (*DB, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("create home dir: %w", err)
	}
	path := filepath.Join(home, "state.db")
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL;`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	if err := applyMigrations(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return &DB{sqlDB}, nil
}

// OpenForRead opens an existing state database for read-only access. Unlike
// Open, it never creates the home directory or database, never applies
// migrations, and never changes the journal mode. The connection is guarded by
// the query_only pragma so it cannot write, letting callers derive status from
// persisted state without mutating it. It returns an error (os.ErrNotExist when
// the database is absent) rather than creating anything.
func OpenForRead(home string) (*DB, error) {
	path := filepath.Join(home, "state.db")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=query_only(true)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite (read-only): %w", err)
	}
	return &DB{sqlDB}, nil
}

func (db *DB) Close() error {
	return db.DB.Close()
}
