package app_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
)

func newCoreFixture(t *testing.T, setup bool) fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	assets := fstest.MapFS{"index.html": {Data: []byte("<html>HomeAtlas</html>")}}
	a, err := app.NewWithModules(context.Background(), db, assets, origin, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	f := fixture{dataDir: dir, db: db, app: a, assets: assets}
	if setup {
		code, err := os.ReadFile(filepath.Join(dir, "setup-code"))
		if err != nil {
			t.Fatal(err)
		}
		w := perform(a, "POST", "/api/setup", map[string]string{"code": strings.TrimSpace(string(code)), "name": "Owner", "username": "core-owner", "password": invitedPassword, "householdName": "Core home", "timezone": "UTC"}, nil)
		if w.Code != 201 {
			t.Fatalf("setup: %d %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &f.state); err != nil {
			t.Fatal(err)
		}
		f.cookie = w.Result().Cookies()[0]
	}
	return f
}

func assertNoDocuments(t *testing.T, f fixture) {
	t.Helper()
	var count int
	if err := f.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name IN ('documents','document_shares','documents_fts')").Scan(&count); err != nil || count != 0 {
		t.Fatalf("Documents schema was created: %d %v", count, err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "documents")); !os.IsNotExist(err) {
		t.Fatalf("Documents directory was created: %v", err)
	}
}

func TestCoreOnlySetupFamilySettingsAndSessions(t *testing.T) {
	f := newCoreFixture(t, true)
	assertNoDocuments(t, f)
	for _, permission := range f.state.Session.Permissions {
		if strings.HasPrefix(permission, "documents.") {
			t.Fatal("Core setup granted Documents permissions")
		}
	}
	for _, path := range []string{"/", "/family", "/settings", "/modules", "/api/health", "/api/household/settings", "/api/household/members", "/api/household/backups/schedule", "/api/household/backups/history"} {
		w := perform(f.app, "GET", path, nil, f.cookie)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/modules", "/api/search?q=manual"} {
		w := perform(f.app, "GET", path, nil, f.cookie)
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := perform(f.app, "GET", "/api/documents", nil, f.cookie); w.Code != 404 {
		t.Fatal("Documents routes mounted")
	}
	if w := perform(f.app, "PATCH", "/api/household/settings", map[string]string{"name": "Updated home", "timezone": "America/Mexico_City"}, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	link := inviteMember(t, f, "core-member@example.test", "member", []string{}, f.cookie)
	member, _ := acceptMember(t, f, link)
	if len(member.Session.Permissions) != 0 {
		t.Fatal("unrequested capabilities granted")
	}
	if w := perform(f.app, "POST", "/api/auth/logout", nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := perform(f.app, "POST", "/api/auth/login", map[string]string{"identifier": "core-owner", "password": invitedPassword}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	assertNoDocuments(t, f)
}

func TestCoreOnlyRestartPreservesDocumentsSharingAndRevocations(t *testing.T) {
	f := newFixture(t)
	reader, readerCookie := acceptMember(t, f, inviteMember(t, f, "reader@example.test", "member", []string{"documents.read"}, f.cookie))
	d := decodeDocument(t, uploadRequest(t, f, "retained.txt", []byte("Original preserved across Core-only restarts"), validMetadata, f.cookie))
	if w := perform(f.app, "PUT", "/api/documents/"+d.ID+"/shares/"+reader.Session.User.ID, nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, err := f.db.Exec("DELETE FROM identity_permissions WHERE user_id=? AND capability='documents.write'", f.state.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate an installation predating the new initial-policy migration.
	if _, err := f.db.Exec("DELETE FROM schema_migrations WHERE module='app.documents-installation-permissions'"); err != nil {
		t.Fatal(err)
	}
	before := perform(f.app, "GET", "/api/documents/"+d.ID+"/shares", nil, f.cookie).Body.String()
	f.app.Close()
	core, err := app.NewWithModules(context.Background(), f.db, f.assets, origin, f.dataDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := perform(core, "GET", "/api/documents/"+d.ID, nil, readerCookie); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := perform(core, "GET", "/api/modules", nil, f.cookie); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	coreFixture := f
	coreFixture.app = core
	backup := backupRequest(coreFixture, f.cookie)
	if backup.Code != 200 {
		t.Fatal(backup.Code, backup.Body.String())
	}
	entries := archiveEntries(t, backup.Body.Bytes())
	if !strings.Contains(string(entries["manifest.json"]), "documents/") {
		t.Fatal("dormant Documents originals omitted from backup")
	}
	core.Close()
	reopened, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	w := perform(reopened, "GET", "/api/documents/"+d.ID+"/download", nil, readerCookie)
	if w.Code != 200 || w.Body.String() != "Original preserved across Core-only restarts" {
		t.Fatal(w.Code, w.Body.String())
	}
	if after := perform(reopened, "GET", "/api/documents/"+d.ID+"/shares", nil, f.cookie).Body.String(); after != before {
		t.Fatal("sharing changed")
	}
	var state identity.State
	w = perform(reopened, "GET", "/api/auth/session", nil, f.cookie)
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Session == nil || slices.Contains(state.Session.Permissions, "documents.write") {
		t.Fatal("session lost or revoked permission restored")
	}
}

func TestCoreOnlyBackupRecoveryAndLaterDocumentsInstallation(t *testing.T) {
	f := newCoreFixture(t, true)
	w := backupRequest(f, f.cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	archive := w.Body.Bytes()
	if !strings.Contains(string(archiveEntries(t, archive)["manifest.json"]), `"coreOnly":true`) {
		t.Fatal("Core-only backup is ambiguous")
	}
	assertNoDocuments(t, f)
	empty := newCoreFixture(t, false)
	code, err := os.ReadFile(filepath.Join(empty.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	w = encryptedRequest(empty, "POST", "/api/setup/restore", archive, base64.StdEncoding.EncodeToString([]byte(testBackupPassword)), strings.TrimSpace(string(code)))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = perform(empty.app, "POST", "/api/auth/login", map[string]string{"identifier": "core-owner", "password": invitedPassword}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	assertNoDocuments(t, empty)
	f.app.Close()
	withDocs, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer withDocs.Close()
	f.app = withDocs
	d := decodeDocument(t, uploadRequest(t, f, "new.txt", []byte("Keep this newer document"), validMetadata, f.cookie))
	w = restoreRequest(f, archive, f.cookie, origin, "replace-household")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = perform(withDocs, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie)
	if w.Code != 200 || w.Body.String() != "Keep this newer document" {
		t.Fatal("Core-only restoration lost later module data", w.Code)
	}
}

func TestCoreOnlyRejectsUnknownAndDuplicateModules(t *testing.T) {
	f := newCoreFixture(t, false)
	for _, names := range [][]string{{"unknown"}, {"documents", "documents"}, {""}} {
		if _, err := app.NewWithModules(context.Background(), f.db, f.assets, origin, f.dataDir, nil, names); err == nil {
			t.Fatal("invalid module selection accepted", names)
		}
	}
	assertNoDocuments(t, f)
}

func TestCoreOnlyRejectsDocumentsRecoveryWithoutMutatingIdentity(t *testing.T) {
	source := newFixture(t)
	w := backupRequest(source, source.cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	f := newCoreFixture(t, false)
	code, err := os.ReadFile(filepath.Join(f.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	w = encryptedRequest(f, "POST", "/api/setup/restore", w.Body.Bytes(), base64.StdEncoding.EncodeToString([]byte(testBackupPassword)), strings.TrimSpace(string(code)))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "backup_module_required") {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	if err := f.db.QueryRow("SELECT count(*) FROM identity_users").Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected recovery changed identity", count, err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "setup-code")); err != nil {
		t.Fatal("setup code consumed", err)
	}
	assertNoDocuments(t, f)
}
