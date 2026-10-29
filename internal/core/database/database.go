package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Migration struct {
	Version       int
	SQL           string
	RebuildTables bool
}

func Open(path string) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Apply commits each module's ordered migrations atomically and detects edited history.
func Apply(ctx context.Context, db *sql.DB, module string, migrations []Migration) (result error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	rebuild := false
	for _, m := range migrations {
		rebuild = rebuild || m.RebuildTables
	}
	if rebuild {
		if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		defer func() {
			if _, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON"); err != nil {
				result = err
			}
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		module TEXT NOT NULL, version INTEGER NOT NULL, checksum TEXT NOT NULL,
		PRIMARY KEY (module, version))`)
	if err != nil {
		return err
	}
	var latest int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations WHERE module = ?", module).Scan(&latest); err != nil {
		return err
	}
	if latest > len(migrations) {
		return fmt.Errorf("%s: database schema is newer than this application", module)
	}
	for i, migration := range migrations {
		if migration.Version != i+1 {
			return fmt.Errorf("%s: migrations must start at 1 and be consecutive", module)
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(migration.SQL)))
		var existing string
		err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE module = ? AND version = ?", module, migration.Version).Scan(&existing)
		if err == nil {
			if existing != checksum {
				return fmt.Errorf("%s: migration %d was modified", module, migration.Version)
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("%s migration %d: %w", module, migration.Version, err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (?, ?, ?)", module, migration.Version, checksum); err != nil {
			return err
		}
	}
	if rebuild {
		rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		invalid := rows.Next()
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if invalid {
			return fmt.Errorf("%s: foreign key violation after table rebuild", module)
		}
	}
	return tx.Commit()
}
