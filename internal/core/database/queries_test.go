package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type unknownTransaction struct{}

func (unknownTransaction) Commit() error   { return nil }
func (unknownTransaction) Rollback() error { return nil }

func TestSharedTransactionAdapterRollsBackEveryRepository(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE first(value TEXT); CREATE TABLE second(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"first", "second"} {
		q, err := Within(db, tx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = q.ExecContext(context.Background(), "INSERT INTO "+table+" VALUES('staged')"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT (SELECT count(*) FROM first)+(SELECT count(*) FROM second)").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial commit: %d %v", count, err)
	}
	if _, err := Within(db, unknownTransaction{}); err == nil {
		t.Fatal("unknown transaction accepted")
	}
	var invalid *sql.Tx
	if _, err := Within(db, invalid); err == nil {
		t.Fatal("nil SQL transaction accepted")
	}
	missing := Normalize(sql.ErrNoRows)
	if !errors.Is(missing, unitofwork.ErrNotFound) || !errors.Is(missing, sql.ErrNoRows) {
		t.Fatal("not-found compatibility lost", missing)
	}
}
