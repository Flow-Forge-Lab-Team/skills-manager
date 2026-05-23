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

func (db *DB) Close() error {
	return db.DB.Close()
}
