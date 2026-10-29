package plugins

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

func storageFixture(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	registry, err := modules.New(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := identity.New(context.Background(), db, identity.Config{PublicURL: "http://127.0.0.1:8080", SetupCodePath: filepath.Join(t.TempDir(), "setup-code"), Modules: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { accounts.Close() })
	_, err = db.Exec(`
 INSERT INTO identity_users(id,name,email,password_hash,username) VALUES('alice','Alice',NULL,'!','alice'),('bob','Bob',NULL,'!','bob');
 INSERT INTO identity_households VALUES('home-a','Home A','UTC'),('home-b','Home B','UTC');
 INSERT INTO module_states VALUES('ext-test',1),('ext-other',1);
 INSERT INTO identity_memberships(user_id,household_id,role) VALUES('alice','home-a','owner'),('alice','home-b','owner'),('bob','home-a','member');
 UPDATE identity_member_ids SET id=user_id||'-'||substr(household_id,6);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []access.Principal{{UserID: "alice", HouseholdID: "home-a"}, {UserID: "alice", HouseholdID: "home-b"}, {UserID: "bob", HouseholdID: "home-a"}} {
		for _, id := range []string{"ext-test", "ext-other"} {
			for _, cap := range []string{"read", "write"} {
				if _, err := db.Exec("INSERT INTO identity_permissions VALUES(?,?,?)", p.UserID, p.HouseholdID, id+"."+cap); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	s, err := NewStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func storageContext(user, home, member string) context.Context {
	return access.WithPrincipal(context.Background(), access.Principal{UserID: user, HouseholdID: home, MembershipID: member})
}
func beginStorage(t *testing.T, s *Store, ctx context.Context, id string, readOnly bool) *storageAction {
	t.Helper()
	p := &Package{Manifest: Manifest{ID: id, Storage: "user"}}
	a, err := s.begin(ctx, p, readOnly)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func setStorage(t *testing.T, a *storageAction, ctx context.Context, key, value string) {
	t.Helper()
	if _, err := a.call(ctx, storageRequest{Op: "set", Key: key, Value: &value}); err != nil {
		t.Fatal(err)
	}
}
func getStorage(t *testing.T, a *storageAction, ctx context.Context, key string) storageResponse {
	t.Helper()
	r, err := a.call(ctx, storageRequest{Op: "get", Key: key})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestStorageNamespacesRevocationMembershipsAndReadOnly(t *testing.T) {
	s := storageFixture(t)
	ctx := storageContext("alice", "home-a", "alice-a")
	a := beginStorage(t, s, ctx, "ext-test", false)
	setStorage(t, a, ctx, "secret", "alice private")
	if err := a.commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ctx    context.Context
		plugin string
	}{{storageContext("bob", "home-a", "bob-a"), "ext-test"}, {storageContext("alice", "home-b", "alice-b"), "ext-test"}, {ctx, "ext-other"}} {
		other := beginStorage(t, s, tc.ctx, tc.plugin, true)
		if r := getStorage(t, other, tc.ctx, "secret"); r.Found {
			t.Fatal("private data crossed namespace")
		}
	}
	reader := beginStorage(t, s, ctx, "ext-test", true)
	if r := getStorage(t, reader, ctx, "secret"); !r.Found || *r.Value != "alice private" {
		t.Fatal(r)
	}
	value := "forbidden"
	if _, err := reader.call(ctx, storageRequest{Op: "set", Key: "secret", Value: &value}); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("read-only view wrote")
	}
	writer := beginStorage(t, s, ctx, "ext-test", false)
	setStorage(t, writer, ctx, "secret", "revoked")
	if _, err := s.db.Exec("DELETE FROM identity_permissions WHERE user_id='alice' AND household_id='home-a' AND capability='ext-test.write'"); err != nil {
		t.Fatal(err)
	}
	if err := writer.commit(ctx); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("write survived revocation", err)
	}
	reader = beginStorage(t, s, ctx, "ext-test", true)
	if r := getStorage(t, reader, ctx, "secret"); *r.Value != "alice private" {
		t.Fatal("revoked write changed data")
	}
	if _, err := s.db.Exec("UPDATE identity_member_ids SET id='alice-rejoined' WHERE id='alice-a'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.begin(ctx, &Package{Manifest: Manifest{ID: "ext-test", Storage: "user"}}, true); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("retired membership read")
	}
	newCtx := storageContext("alice", "home-a", "alice-rejoined")
	reader = beginStorage(t, s, newCtx, "ext-test", true)
	if r := getStorage(t, reader, newCtx, "secret"); r.Found {
		t.Fatal("reinvitation resurrected private data")
	}
	if _, err := s.db.Exec("INSERT INTO household_module_states VALUES('home-a','ext-test',0)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.begin(newCtx, &Package{Manifest: Manifest{ID: "ext-test", Storage: "user"}}, true); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("inactive module read storage")
	}
}

func TestStorageOptimisticCommitPaginationAndBudgets(t *testing.T) {
	s := storageFixture(t)
	ctx := storageContext("alice", "home-a", "alice-a")
	first := beginStorage(t, s, ctx, "ext-test", false)
	second := beginStorage(t, s, ctx, "ext-test", false)
	setStorage(t, first, ctx, "same", "first")
	setStorage(t, second, ctx, "same", "second")
	if err := first.commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.commit(ctx); !errors.Is(err, ErrStorageConflict) {
		t.Fatal("lost update allowed", err)
	}
	a := beginStorage(t, s, ctx, "ext-test", false)
	for i := range 50 {
		setStorage(t, a, ctx, fmt.Sprintf("key-%02d", i), "value")
	}
	if err := a.commit(ctx); err != nil {
		t.Fatal(err)
	}
	a = beginStorage(t, s, ctx, "ext-test", false)
	if _, err := a.call(ctx, storageRequest{Op: "delete", Key: "key-01"}); err != nil {
		t.Fatal(err)
	}
	setStorage(t, a, ctx, "key-00a", "new")
	r, err := a.call(ctx, storageRequest{Op: "list", Limit: 2})
	if err != nil || len(r.Keys) != 2 || r.Keys[0] != "key-00" || r.Keys[1] != "key-00a" || r.Next != "key-00a" {
		t.Fatal(r, err)
	}
	r, err = a.call(ctx, storageRequest{Op: "list", After: r.Next, Limit: 2})
	if err != nil || r.Keys[0] != "key-02" {
		t.Fatal(r, err)
	}
	for _, req := range []storageRequest{{Op: "get", Key: "../escape"}, {Op: "get", Key: "x", Value: new(string)}, {Op: "set", Key: "x", Value: nil}, {Op: "list", Limit: 65}, {Op: "get", Key: "x", After: "x"}} {
		if _, err := a.call(ctx, req); err == nil {
			t.Fatal("invalid request accepted", req)
		}
	}
	large := strings.Repeat("x", StorageValueBytes+1)
	if _, err := a.call(ctx, storageRequest{Op: "set", Key: "large", Value: &large}); err == nil {
		t.Fatal("unbounded record")
	}
	a = beginStorage(t, s, ctx, "ext-test", true)
	for range StorageCalls {
		if _, err := a.call(ctx, storageRequest{Op: "get", Key: "same"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.call(ctx, storageRequest{Op: "get", Key: "same"}); err == nil {
		t.Fatal("unbounded host calls")
	}
}

func i32(n int) []byte {
	b := uleb(n)
	if b[len(b)-1]&0x40 != 0 {
		b[len(b)-1] |= 0x80
		b = append(b, 0)
	}
	return append([]byte{0x41}, b...)
}
func storageCommand(requests []string, ending string) []byte {
	b := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	b = append(b, section(1, []byte{2, 0x60, 4, 0x7f, 0x7f, 0x7f, 0x7f, 1, 0x7f, 0x60, 0, 0})...)
	imp := []byte{2}
	for _, f := range [][2]string{{"wasi_snapshot_preview1", "fd_write"}, {"homeatlas_storage_v1", "call"}} {
		imp = append(imp, wasmString(f[0])...)
		imp = append(imp, wasmString(f[1])...)
		imp = append(imp, 0, 0)
	}
	b = append(b, section(2, imp)...)
	b = append(b, section(3, []byte{1, 1})...)
	b = append(b, section(5, []byte{1, 0, 4})...)
	exp := append([]byte{2}, wasmString("memory")...)
	exp = append(exp, 2, 0)
	exp = append(exp, wasmString("_start")...)
	exp = append(exp, 0, 2)
	b = append(b, section(7, exp)...)
	code := []byte{0}
	data := make([]byte, 1024)
	offset := 1024
	for _, req := range requests {
		for _, n := range []int{offset, len(req), 65536, StorageRPCBytes} {
			code = append(code, i32(n)...)
		}
		code = append(code, 0x10, 1, 0x1a)
		data = append(data, []byte(req)...)
		offset += len(req)
	}
	switch ending {
	case "loop":
		code = append(code, 0x03, 0x40, 0x0c, 0, 0x0b)
	case "trap":
		code = append(code, 0x00)
	}
	result := `{"text":"ok"}`
	if ending == "invalid" {
		result = `not JSON`
	}
	data = append(data, []byte(result)...)
	code = append(code, i32(0)...)
	code = append(code, i32(offset)...)
	code = append(code, 0x36, 2, 0)
	code = append(code, i32(4)...)
	code = append(code, i32(len(result))...)
	code = append(code, 0x36, 2, 0)
	for _, n := range []int{1, 0, 1, 8} {
		code = append(code, i32(n)...)
	}
	code = append(code, 0x10, 0, 0x1a, 0x0b)
	b = append(b, section(10, append(append([]byte{1}, uleb(len(code))...), code...))...)
	segment := []byte{1, 0, 0x41, 0, 0x0b}
	segment = append(segment, uleb(len(data))...)
	segment = append(segment, data...)
	return append(b, section(11, segment)...)
}
func storagePackage(t *testing.T, requests []string, ending string, readOnly bool) *Package {
	t.Helper()
	p, err := Read(packageBytes(t, storageCommand(requests, ending), func(m *Manifest) { m.Storage = "user"; m.Views[0].ReadOnly = readOnly }, ""))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStorageWASIBridgeStagesAtomicallyAndUsesCurrentCaller(t *testing.T) {
	s := storageFixture(t)
	e := &Engine{Store: s}
	defer e.Close()
	ctx := storageContext("alice", "home-a", "alice-a")
	set := `{"op":"set","key":"secret","value":"private"}`
	for _, ending := range []string{"invalid", "trap", "loop"} {
		deadline, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		if _, err := e.Run(deadline, storagePackage(t, []string{set}, ending, false), "calculate", nil); err == nil {
			t.Fatal("failed guest committed", ending)
		}
		cancel()
		var count int
		s.db.QueryRow("SELECT count(*) FROM plugin_storage_records").Scan(&count)
		if count != 0 {
			t.Fatal("failed guest leaked mutation", ending)
		}
	}
	p := storagePackage(t, []string{set}, "", false)
	if err := validateGuest(ctx, p.wasm, true); err != nil {
		t.Fatal(err)
	}
	if err := validateWasm(ctx, p.wasm); err == nil {
		t.Fatal("storage import accepted without manifest consent")
	}
	for _, scope := range []context.Context{ctx, storageContext("bob", "home-a", "bob-a"), storageContext("alice", "home-b", "alice-b")} {
		if _, err := e.Run(scope, p, "calculate", nil); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM plugin_storage_records").Scan(&count); err != nil || count != 3 {
		t.Fatal("cached guest used wrong caller scope", count, err)
	}
	if _, err := e.Run(ctx, storagePackage(t, []string{`{"op":"set","key":"bad","value":"x"}`, `{"op":"get","key":"../escape"}`}, "", false), "calculate", nil); err == nil {
		t.Fatal("ignored host failure committed")
	}
	if err := s.db.QueryRow("SELECT count(*) FROM plugin_storage_records WHERE key='bad'").Scan(&count); err != nil || count != 0 {
		t.Fatal("ignored failure leaked writes")
	}
	if _, err := e.Run(ctx, storagePackage(t, []string{set}, "", true), "calculate", nil); !errors.Is(err, ErrStorageDenied) {
		t.Fatal("read-only guest wrote", err)
	}
}

func TestStorageCommitRollsBackAllStatements(t *testing.T) {
	s := storageFixture(t)
	ctx := storageContext("alice", "home-a", "alice-a")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_storage BEFORE INSERT ON plugin_storage_records WHEN new.key='fail' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	a := beginStorage(t, s, ctx, "ext-test", false)
	setStorage(t, a, ctx, "good", "value")
	setStorage(t, a, ctx, "fail", "value")
	if err := a.commit(ctx); err == nil {
		t.Fatal("failed transaction committed")
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM plugin_storage_records").Scan(&count)
	if count != 0 {
		t.Fatal("partial records committed")
	}
	s.db.QueryRow("SELECT count(*) FROM plugin_storage_scopes").Scan(&count)
	if count != 0 {
		t.Fatal("partial namespace committed")
	}
}

// Keep the RPC schema strict: callers cannot select a foreign identity.
func TestStorageRPCRejectsForeignNamespaceFields(t *testing.T) {
	var req storageRequest
	if err := decode([]byte(`{"op":"get","key":"secret","household":"other"}`), &req); err == nil {
		t.Fatal("guest chose household")
	}
}
