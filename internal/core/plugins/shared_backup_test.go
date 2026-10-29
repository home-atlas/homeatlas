package plugins

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
)

func sharedSnapshot(t *testing.T, s *Store, b Backup) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, schema := range append(b.Tables(), "CREATE TABLE members(id TEXT PRIMARY KEY,member_id TEXT UNIQUE)") {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query("SELECT user_id,id FROM identity_member_ids WHERE household_id='home-a'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var user, member string
		rows.Scan(&user, &member)
		if _, err := db.Exec("INSERT INTO members VALUES(?,?)", user, member); err != nil {
			rows.Close()
			t.Fatal(err)
		}
	}
	rows.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Export(context.Background(), s.db, tx, "home-a", false); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Inspect(context.Background(), db, ""); err != nil {
		t.Fatal(err)
	}
	return &Store{db: db}
}
func restoreSharedSnapshot(t *testing.T, s *Store, b Backup, source *Store, recovery map[string]string) {
	t.Helper()
	plan, err := b.Prepare(context.Background(), source.db, "", "home-a")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(context.Background(), tx, backup.RestoreContext{HouseholdID: "home-a", RecoveryMemberships: recovery}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	plan.Finish(true, false)
}
func TestSharedRestoreIntersectsCurrentRightsAndRecordIdentity(t *testing.T) {
	s := storageFixture(t)
	grantSharing(t, s)
	alice := storageContext("alice", "home-a", "alice-a")
	bob := storageContext("bob", "home-a", "bob-a")
	for _, key := range []string{"revoked", "downgraded", "unchanged", "replaced"} {
		sharedSet(t, s, alice, key, "snapshot")
		sharedGrantTo(t, s, alice, key, "bob", "write")
	}
	p := testPackage(t, false)
	p.Manifest.Storage = "shared"
	b := Backup{p}
	source := sharedSnapshot(t, s, b)
	a := sharedAction(t, s, alice, false)
	sharedCall(t, a, alice, storageRequest{Op: "shared.revoke", Key: "revoked", User: "bob"})
	if err := a.commit(alice); err != nil {
		t.Fatal(err)
	}
	sharedGrantTo(t, s, alice, "downgraded", "bob", "read")
	a = sharedAction(t, s, alice, false)
	sharedCall(t, a, alice, storageRequest{Op: "shared.delete", Key: "replaced"})
	if err := a.commit(alice); err != nil {
		t.Fatal(err)
	}
	sharedSet(t, s, alice, "replaced", "new record")
	sharedGrantTo(t, s, alice, "replaced", "bob", "read")
	restoreSharedSnapshot(t, s, b, source, nil)
	for _, key := range []string{"revoked", "replaced"} {
		a = sharedAction(t, s, bob, true)
		if r := sharedCall(t, a, bob, storageRequest{Op: "shared.get", Key: key}); r.Found {
			t.Fatal("historical ACL revived", key)
		}
	}
	a = sharedAction(t, s, bob, true)
	if r := sharedCall(t, a, bob, storageRequest{Op: "shared.get", Key: "downgraded"}); !r.Found || *r.Value != "snapshot" {
		t.Fatal(r)
	}
	var permission string
	if err := s.db.QueryRow("SELECT permission FROM plugin_shared_grants WHERE user_id='bob' AND key='downgraded'").Scan(&permission); err != nil || permission != "read" {
		t.Fatal("downgrade lost", permission, err)
	}
	sharedSet(t, s, bob, "unchanged", "editor still authorized")
	var staging int
	s.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name LIKE '%current_acl'").Scan(&staging)
	if staging != 0 {
		t.Fatal("restore staging persisted")
	}
}

func TestSharedRecoveryPreservesReadersOfRetiredCreatorWithoutRevivingOwner(t *testing.T) {
	s := storageFixture(t)
	grantSharing(t, s)
	alice := storageContext("alice", "home-a", "alice-a")
	bob := storageContext("bob", "home-a", "bob-a")
	sharedSet(t, s, alice, "retained", "shared household data")
	sharedGrantTo(t, s, alice, "retained", "bob", "read")
	if _, err := s.db.Exec("DELETE FROM identity_memberships WHERE user_id='alice' AND household_id='home-a'"); err != nil {
		t.Fatal(err)
	}
	p := testPackage(t, false)
	p.Manifest.Storage = "shared"
	b := Backup{p}
	source := sharedSnapshot(t, s, b)
	if _, err := s.db.Exec("UPDATE identity_member_ids SET id='bob-new' WHERE id='bob-a'"); err != nil {
		t.Fatal(err)
	}
	restoreSharedSnapshot(t, s, b, source, map[string]string{"bob": "bob-a"})
	bob = storageContext("bob", "home-a", "bob-new")
	a := sharedAction(t, s, bob, true)
	r := sharedCall(t, a, bob, storageRequest{Op: "shared.get", Key: "retained"})
	if !r.Found || *r.Value != "shared household data" {
		t.Fatal("retired creator lost shared data", r)
	}
	var ownerMember string
	s.db.QueryRow("SELECT owner_member_id FROM plugin_shared_records WHERE key='retained'").Scan(&ownerMember)
	if ownerMember != "" {
		t.Fatal("retired owner was rebound")
	}
	// A new membership for the same creator receives no historical ownership.
	if _, err := s.db.Exec("INSERT INTO identity_memberships(user_id,household_id,role) VALUES('alice','home-a','owner'); INSERT INTO identity_permissions VALUES('alice','home-a','ext-test.read')"); err != nil {
		t.Fatal(err)
	}
	var member string
	s.db.QueryRow("SELECT id FROM identity_member_ids WHERE user_id='alice' AND household_id='home-a'").Scan(&member)
	alice = storageContext("alice", "home-a", member)
	a = sharedAction(t, s, alice, true)
	if sharedCall(t, a, alice, storageRequest{Op: "shared.get", Key: "retained"}).Found {
		t.Fatal("rejoined creator revived access")
	}
}

func TestSharedRestoreFailureRollsBackRecordsAndACL(t *testing.T) {
	s := storageFixture(t)
	grantSharing(t, s)
	alice := storageContext("alice", "home-a", "alice-a")
	sharedSet(t, s, alice, "record", "snapshot")
	sharedGrantTo(t, s, alice, "record", "bob", "write")
	p := testPackage(t, false)
	p.Manifest.Storage = "shared"
	b := Backup{p}
	source := sharedSnapshot(t, s, b)
	sharedSet(t, s, alice, "record", "live")
	sharedGrantTo(t, s, alice, "record", "bob", "read")
	if _, err := s.db.Exec("CREATE TRIGGER fail_shared_restore BEFORE INSERT ON plugin_shared_grants BEGIN SELECT RAISE(ABORT,'test'); END"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan, err := b.Prepare(ctx, source.db, "", "home-a")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = plan.Apply(ctx, tx, backup.RestoreContext{HouseholdID: "home-a"})
	tx.Rollback()
	plan.Finish(false, false)
	if err == nil {
		t.Fatal("failed restore committed")
	}
	var value, permission string
	if err := s.db.QueryRow("SELECT value FROM plugin_shared_records WHERE key='record'").Scan(&value); err != nil || value != "live" {
		t.Fatal(value, err)
	}
	if err := s.db.QueryRow("SELECT permission FROM plugin_shared_grants WHERE key='record'").Scan(&permission); err != nil || permission != "read" {
		t.Fatal(permission, err)
	}
	var staging int
	s.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name LIKE '%current_acl'").Scan(&staging)
	if staging != 0 {
		t.Fatal("failed restore leaked staging")
	}
}
