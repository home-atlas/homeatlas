package plugins

import (
	"context"
	"errors"
	"testing"
)

func sharedAction(t *testing.T, s *Store, ctx context.Context, readOnly bool) *storageAction {
	t.Helper()
	a, err := s.begin(ctx, &Package{Manifest: Manifest{ID: "ext-test", Storage: "shared"}}, readOnly)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func sharedCall(t *testing.T, a *storageAction, ctx context.Context, req storageRequest) storageResponse {
	t.Helper()
	r, err := a.call(ctx, req)
	if err != nil {
		t.Fatal(req, err)
	}
	return r
}
func sharedSet(t *testing.T, s *Store, ctx context.Context, key, value string) {
	t.Helper()
	a := sharedAction(t, s, ctx, false)
	sharedCall(t, a, ctx, storageRequest{Op: "shared.set", Key: key, Value: &value})
	if err := a.commit(ctx); err != nil {
		t.Fatal(err)
	}
}
func sharedGrantTo(t *testing.T, s *Store, ctx context.Context, key, user, permission string) {
	t.Helper()
	a := sharedAction(t, s, ctx, false)
	sharedCall(t, a, ctx, storageRequest{Op: "shared.grant", Key: key, User: user, Permission: permission})
	if err := a.commit(ctx); err != nil {
		t.Fatal(err)
	}
}
func grantSharing(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec("INSERT INTO identity_permissions SELECT user_id,household_id,'ext-test.share' FROM identity_memberships"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedRecordsACLRevocationIsolationAndMemberships(t *testing.T) {
	s := storageFixture(t)
	grantSharing(t, s)
	alice := storageContext("alice", "home-a", "alice-a")
	bob := storageContext("bob", "home-a", "bob-a")
	other := storageContext("alice", "home-b", "alice-b")
	sharedSet(t, s, alice, "same", "original")
	foreign, err := s.begin(alice, &Package{Manifest: Manifest{ID: "ext-other", Storage: "shared"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if sharedCall(t, foreign, alice, storageRequest{Op: "shared.get", Key: "same"}).Found {
		t.Fatal("shared record crossed plugin boundary")
	}
	sharedSet(t, s, other, "other-home", "separate")
	// Bob belongs only to home-a; a grant cannot cross the household boundary.
	aOther := sharedAction(t, s, other, false)
	if _, err := aOther.call(other, storageRequest{Op: "shared.grant", Key: "other-home", User: "bob", Permission: "read"}); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("recipient outside household accepted", err)
	}
	for _, ctx := range []context.Context{bob, other} {
		a := sharedAction(t, s, ctx, true)
		if r := sharedCall(t, a, ctx, storageRequest{Op: "shared.get", Key: "same"}); r.Found {
			t.Fatal("unshared record exposed")
		}
	}
	sharedGrantTo(t, s, alice, "same", "bob", "read")
	a := sharedAction(t, s, bob, true)
	r := sharedCall(t, a, bob, storageRequest{Op: "shared.get", Key: "same"})
	if !r.Found || *r.Value != "original" {
		t.Fatal(r)
	}
	value := "forbidden"
	a = sharedAction(t, s, bob, false)
	if _, err := a.call(bob, storageRequest{Op: "shared.set", Key: "same", Value: &value}); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("reader edited", err)
	}
	sharedGrantTo(t, s, alice, "same", "bob", "write")
	sharedSet(t, s, bob, "same", "edited")
	for _, req := range []storageRequest{{Op: "shared.grant", Key: "same", User: "alice", Permission: "read"}, {Op: "shared.revoke", Key: "same", User: "alice"}, {Op: "shared.delete", Key: "same"}} {
		a = sharedAction(t, s, bob, false)
		if _, err := a.call(bob, req); !errors.Is(err, ErrStorageDenied) {
			t.Fatal("editor managed ownership", req, err)
		}
	}
	inflight := sharedAction(t, s, bob, true)
	sharedCall(t, inflight, bob, storageRequest{Op: "shared.get", Key: "same"})
	a = sharedAction(t, s, alice, false)
	sharedCall(t, a, alice, storageRequest{Op: "shared.revoke", Key: "same", User: "bob"})
	if err := a.commit(alice); err != nil {
		t.Fatal(err)
	}
	if err := inflight.commit(bob); !errors.Is(err, ErrStorageConflict) {
		t.Fatal("revoked in-flight read returned", err)
	}
	a = sharedAction(t, s, bob, true)
	if r := sharedCall(t, a, bob, storageRequest{Op: "shared.get", Key: "same"}); r.Found {
		t.Fatal("revoked read succeeded")
	}
	sharedGrantTo(t, s, alice, "same", "bob", "write")
	if _, err := s.db.Exec("UPDATE identity_member_ids SET id='bob-rejoined' WHERE id='bob-a'"); err != nil {
		t.Fatal(err)
	}
	bob = storageContext("bob", "home-a", "bob-rejoined")
	a = sharedAction(t, s, bob, true)
	if r := sharedCall(t, a, bob, storageRequest{Op: "shared.get", Key: "same"}); r.Found {
		t.Fatal("reinvitation revived grant")
	}
	sharedGrantTo(t, s, alice, "same", "bob", "read")
	if _, err := s.db.Exec("DELETE FROM identity_permissions WHERE user_id='bob' AND capability='ext-test.read'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.begin(bob, &Package{Manifest: Manifest{ID: "ext-test", Storage: "shared"}}, true); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("revoked module permission read", err)
	}
}

func TestSharedListsReplacementAtomicityAndConsent(t *testing.T) {
	s := storageFixture(t)
	grantSharing(t, s)
	alice := storageContext("alice", "home-a", "alice-a")
	bob := storageContext("bob", "home-a", "bob-a")
	sharedSet(t, s, alice, "one", "one")
	sharedSet(t, s, alice, "two", "two")
	sharedGrantTo(t, s, alice, "two", "bob", "read")
	a := sharedAction(t, s, bob, true)
	r := sharedCall(t, a, bob, storageRequest{Op: "shared.list", Limit: 1})
	if len(r.Keys) != 1 || r.Keys[0] != "two" {
		t.Fatal("list leaked hidden keys", r)
	}
	a = sharedAction(t, s, alice, true)
	r = sharedCall(t, a, alice, storageRequest{Op: "shared.grants", Key: "two"})
	if len(r.Grants) != 1 || r.Grants[0].User != "bob" {
		t.Fatal(r)
	}
	if _, err := a.call(alice, storageRequest{Op: "shared.revoke", Key: "two", User: "bob"}); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("readonly modified grants")
	}
	// A replacement has a new immutable ID and does not inherit the old ACL.
	a = sharedAction(t, s, alice, false)
	sharedCall(t, a, alice, storageRequest{Op: "shared.delete", Key: "two"})
	v := "replacement"
	sharedCall(t, a, alice, storageRequest{Op: "shared.set", Key: "two", Value: &v})
	if err := a.commit(alice); err != nil {
		t.Fatal(err)
	}
	a = sharedAction(t, s, bob, true)
	if r := sharedCall(t, a, bob, storageRequest{Op: "shared.get", Key: "two"}); r.Found {
		t.Fatal("replacement inherited grant")
	}
	private := beginStorage(t, s, alice, "ext-test", false)
	if _, err := private.call(alice, storageRequest{Op: "shared.get", Key: "one"}); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("private package used shared host API")
	}
	if _, err := s.db.Exec("CREATE TRIGGER fail_shared BEFORE INSERT ON plugin_shared_grants BEGIN SELECT RAISE(ABORT,'test'); END"); err != nil {
		t.Fatal(err)
	}
	a = sharedAction(t, s, alice, false)
	v = "not committed"
	sharedCall(t, a, alice, storageRequest{Op: "shared.set", Key: "one", Value: &v})
	sharedCall(t, a, alice, storageRequest{Op: "shared.grant", Key: "one", User: "bob", Permission: "read"})
	if err := a.commit(alice); err == nil {
		t.Fatal("failed ACL transaction committed")
	}
	a = sharedAction(t, s, alice, true)
	r = sharedCall(t, a, alice, storageRequest{Op: "shared.get", Key: "one"})
	if *r.Value != "one" {
		t.Fatal("partial shared data committed")
	}
}

func TestSharedGrantListingRechecksShareAtCommit(t *testing.T) {
	s := storageFixture(t)
	grantSharing(t, s)
	alice := storageContext("alice", "home-a", "alice-a")
	sharedSet(t, s, alice, "record", "value")
	a := sharedAction(t, s, alice, true)
	sharedCall(t, a, alice, storageRequest{Op: "shared.grants", Key: "record"})
	if _, err := s.db.Exec("DELETE FROM identity_permissions WHERE user_id='alice' AND household_id='home-a' AND capability='ext-test.share'"); err != nil {
		t.Fatal(err)
	}
	if err := a.commit(alice); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("revoked grant listing returned", err)
	}
}

func TestSharedAndPrivateWritesRollBackTogether(t *testing.T) {
	s := storageFixture(t)
	alice := storageContext("alice", "home-a", "alice-a")
	sharedSet(t, s, alice, "record", "original")
	if _, err := s.db.Exec("CREATE TRIGGER fail_private_after_shared BEFORE INSERT ON plugin_storage_records BEGIN SELECT RAISE(ABORT,'test'); END"); err != nil {
		t.Fatal(err)
	}
	a := sharedAction(t, s, alice, false)
	value := "discarded"
	sharedCall(t, a, alice, storageRequest{Op: "shared.set", Key: "record", Value: &value})
	sharedCall(t, a, alice, storageRequest{Op: "set", Key: "private", Value: &value})
	if err := a.commit(alice); err == nil {
		t.Fatal("mixed failed transaction committed")
	}
	a = sharedAction(t, s, alice, true)
	if r := sharedCall(t, a, alice, storageRequest{Op: "shared.get", Key: "record"}); r.Value == nil || *r.Value != "original" {
		t.Fatal("shared update survived failed private write", r)
	}
	if r := sharedCall(t, a, alice, storageRequest{Op: "get", Key: "private"}); r.Found {
		t.Fatal("failed private write persisted")
	}
}
