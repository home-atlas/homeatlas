package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrationsAreAtomicAndImmutable(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	first := Migration{Version: 1, SQL: "CREATE TABLE example (id INTEGER PRIMARY KEY)"}
	if err := Apply(ctx, db, "example", []Migration{first}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db, "example", nil); err == nil {
		t.Fatal("application with older schema was accepted")
	}
	if err := Apply(ctx, db, "example", []Migration{first}); err != nil {
		t.Fatal(err)
	}
	changed := Migration{Version: 1, SQL: "CREATE TABLE example (name TEXT)"}
	if err := Apply(ctx, db, "example", []Migration{changed}); err == nil {
		t.Fatal("modified migration was accepted")
	}
	broken := Migration{Version: 2, SQL: "CREATE TABLE rollback_probe(id TEXT); INVALID SQL;"}
	if err := Apply(ctx, db, "example", []Migration{first, broken}); err == nil {
		t.Fatal("invalid SQL succeeded")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='rollback_probe'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("migration was not rolled back: count=%d, err=%v", count, err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL not enabled: %q, %v", mode, err)
	}
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&count); err != nil || count != 1 {
		t.Fatalf("foreign keys not enabled: %d, %v", count, err)
	}
}

func TestTableRebuildPreservesChildrenAndFailsClosed(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "rebuild.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	first := Migration{Version: 1, SQL: `CREATE TABLE parent(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE);CREATE TABLE child(id TEXT PRIMARY KEY,parent_id TEXT REFERENCES parent(id) ON DELETE CASCADE);INSERT INTO parent VALUES('person','person@example.test');INSERT INTO child VALUES('document','person');`}
	if err = Apply(ctx, db, "rebuild", []Migration{first}); err != nil {
		t.Fatal(err)
	}
	second := Migration{Version: 2, RebuildTables: true, SQL: `CREATE TABLE parent_new(id TEXT PRIMARY KEY,email TEXT UNIQUE);INSERT INTO parent_new SELECT * FROM parent;DROP TABLE parent;ALTER TABLE parent_new RENAME TO parent;`}
	if err = Apply(ctx, db, "rebuild", []Migration{first, second}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM child WHERE parent_id='person'").Scan(&count); err != nil || count != 1 {
		t.Fatal("rebuild deleted children", count, err)
	}
	broken := Migration{Version: 3, RebuildTables: true, SQL: `DELETE FROM parent`}
	if err = Apply(ctx, db, "rebuild", []Migration{first, second, broken}); err == nil {
		t.Fatal("dangling references accepted")
	}
	if err = db.QueryRow("SELECT count(*) FROM parent").Scan(&count); err != nil || count != 1 {
		t.Fatal("failed rebuild was not rolled back")
	}
	if err = db.QueryRow("PRAGMA foreign_keys").Scan(&count); err != nil || count != 1 {
		t.Fatal("foreign key enforcement not restored")
	}
}
