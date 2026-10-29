package plugins

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
)

func TestPersistentBackupFiltersMembershipsAndRestoresOnlyIncludedNamespace(t *testing.T) {
	s := storageFixture(t)
	ctx := storageContext("alice", "home-a", "alice-a")
	for _, tc := range []struct {
		ctx       context.Context
		id, value string
	}{{ctx, "ext-test", "original"}, {storageContext("alice", "home-b", "alice-b"), "ext-test", "other home"}, {ctx, "ext-other", "other plugin"}, {storageContext("bob", "home-a", "bob-a"), "ext-test", "retired"}} {
		a := beginStorage(t, s, tc.ctx, tc.id, false)
		setStorage(t, a, tc.ctx, "same", tc.value)
		if err := a.commit(tc.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("DELETE FROM identity_memberships WHERE user_id='bob'"); err != nil {
		t.Fatal(err)
	}
	p := testPackage(t, true)
	p.Manifest.Storage = "user"
	provider := Backup{p}
	snapshot, err := database.Open(filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, schema := range append(provider.Tables(), "CREATE TABLE members(id TEXT PRIMARY KEY,member_id TEXT UNIQUE)") {
		if _, err := snapshot.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := snapshot.Exec("INSERT INTO members VALUES('alice','alice-a')"); err != nil {
		t.Fatal(err)
	}
	tx, err := snapshot.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Export(ctx, s.db, tx, "home-a", false); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	contents, err := provider.Inspect(ctx, snapshot, "")
	if err != nil || contents.Records != 1 {
		t.Fatal("excluded namespaces exported", contents, err)
	}
	a := beginStorage(t, s, ctx, "ext-test", false)
	setStorage(t, a, ctx, "same", "later")
	if err := a.commit(ctx); err != nil {
		t.Fatal(err)
	}
	plan, err := provider.Prepare(ctx, snapshot, "", "home-a")
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(ctx, live, backup.RestoreContext{HouseholdID: "home-a"}); err != nil {
		live.Rollback()
		t.Fatal(err)
	}
	if err := live.Commit(); err != nil {
		t.Fatal(err)
	}
	plan.Finish(true, false)
	for _, tc := range []struct {
		ctx       context.Context
		id, value string
	}{{ctx, "ext-test", "original"}, {storageContext("alice", "home-b", "alice-b"), "ext-test", "other home"}, {ctx, "ext-other", "other plugin"}} {
		a := beginStorage(t, s, tc.ctx, tc.id, true)
		r := getStorage(t, a, tc.ctx, "same")
		if !r.Found || *r.Value != tc.value {
			t.Fatal("restore crossed namespace", r)
		}
	}
	// Archive data is untrusted even though the schema and package are pinned.
	for _, statement := range []string{
		"UPDATE " + provider.recordsTable() + " SET user_id='bob'",
		"UPDATE " + provider.recordsTable() + " SET key='../escape'",
		"UPDATE " + provider.recordsTable() + " SET value=?",
	} {
		if _, err := snapshot.Exec("UPDATE " + provider.recordsTable() + " SET user_id='alice',key='same',value='original'"); err != nil {
			t.Fatal(err)
		}
		var err error
		if strings.Contains(statement, "?") {
			_, err = snapshot.Exec(statement, strings.Repeat("x", StorageValueBytes+1))
		} else {
			_, err = snapshot.Exec(statement)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.Inspect(ctx, snapshot, ""); !errors.Is(err, backup.Invalid) {
			t.Fatal("invalid private snapshot accepted", statement, err)
		}
	}
}
