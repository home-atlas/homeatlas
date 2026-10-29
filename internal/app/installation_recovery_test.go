package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
)

func emptyRecoveryTarget(t *testing.T, source fixture) (fixture, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, err := app.New(context.Background(), db, source.assets, origin, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	code, err := os.ReadFile(filepath.Join(dir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	return fixture{dataDir: dir, app: a, db: db, assets: source.assets}, strings.TrimSpace(string(code))
}
func recoverNewRequest(f fixture, archive []byte, code, originHeader string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", origin+"/api/setup/restore", bytes.NewReader(archive))
	if bytes.HasPrefix(archive, []byte("PK\x03\x04")) {
		r.Header.Set("X-HomeAtlas-Allow-Legacy-Backup", "true")
	}
	r.Header.Set("Content-Type", "application/zip")
	r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(testBackupPassword)))
	r.Header.Set("Origin", originHeader)
	r.Header.Set("X-HomeAtlas-Confirm-Restore", "initialize-installation")
	r.Header.Set("X-HomeAtlas-Setup-Code", code)
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, r)
	return w
}
func loginRecovered(t *testing.T, f fixture, email, password string) *http.Cookie {
	t.Helper()
	w := perform(f.app, "POST", "/api/auth/login", map[string]string{"email": email, "password": password}, nil)
	if w.Code != 200 {
		t.Fatal("restored login failed", email, w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}
func assertEmptyRecovery(t *testing.T, f fixture) {
	t.Helper()
	for _, table := range []string{"identity_installation", "identity_users", "identity_households", "identity_memberships", "documents"} {
		var count int
		if err := f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed recovery left live data", table, count, err)
		}
	}
	files, err := os.ReadDir(filepath.Join(f.dataDir, "documents"))
	if err != nil || len(files) != 0 {
		t.Fatal("failed recovery left originals", err, len(files))
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "setup-code")); err != nil {
		t.Fatal("failed recovery consumed setup code", err)
	}
}

func TestCompleteRecoveryAfterLossOfOriginalServer(t *testing.T) {
	source := newFixture(t)
	home := source.state.Session.Household.ID
	owner := source.state.Session.User.ID
	reader := otherMember(t, source, "reader", home)
	admin := otherMember(t, source, "admin", home)
	otherMember(t, source, "retired", home)
	otherMember(t, source, "neighbor", "neighbor-home")
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE identity_users SET password_hash=(SELECT password_hash FROM identity_users WHERE id=?) WHERE id IN ('reader','admin')`, []any{owner}},
		{`UPDATE identity_memberships SET role='admin' WHERE user_id='admin'`, nil},
		{`INSERT INTO identity_permissions VALUES('admin',?,'household.manage')`, []any{home}},
		{`DELETE FROM identity_permissions WHERE user_id='reader' AND capability='documents.write'`, nil},
		{`DELETE FROM identity_memberships WHERE user_id='retired'`, nil},
		{`INSERT INTO identity_verified_emails VALUES(?, 'alice@example.test',1)`, []any{owner}},
	} {
		if _, err := source.db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte("Original para restauración completa")
	d := decodeDocument(t, uploadRequest(t, source, "shared.txt", data, validMetadata, source.cookie))
	private := decodeDocument(t, uploadRequest(t, source, "admin.txt", []byte("Privado del administrador"), validMetadata, admin))
	stale := decodeDocument(t, uploadRequest(t, source, "stale.txt", []byte("No reactivar grant obsoleto"), validMetadata, source.cookie))
	if w := perform(source.app, "PUT", "/api/documents/"+d.ID+"/shares/reader", nil, source.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, err := source.db.Exec("INSERT INTO document_shares(document_id,household_id,user_id,member_id) VALUES(?,?,'reader','obsolete-membership')", stale.ID, home); err != nil {
		t.Fatal(err)
	}
	if w := perform(source.app, "POST", "/api/household/invitations", map[string]any{"email": "pending@example.test", "role": "member", "permissions": []string{"documents.read"}}, source.cookie); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	oldRecovery, _, err := identity.IssueRecovery(context.Background(), source.db, "alice@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.Exec("DELETE FROM identity_permissions WHERE user_id=? AND capability IN ('documents.write','documents.share')", owner); err != nil {
		t.Fatal(err)
	}
	if w := perform(source.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": false}, source.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	archive := backupRequest(source, source.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	var manifest map[string]any
	if err = json.Unmarshal(archiveEntries(t, archive.Body.Bytes())["manifest.json"], &manifest); err != nil || manifest["version"] != float64(2) {
		t.Fatal("full recovery format missing", err)
	}
	var oldMemberID string
	if err = source.db.QueryRow("SELECT id FROM identity_member_ids WHERE user_id='reader'").Scan(&oldMemberID); err != nil {
		t.Fatal(err)
	}
	target, code := emptyRecoveryTarget(t, source)
	// The archive alone must suffice: the source database and filesystem are no longer available.
	source.app.Close()
	source.db.Close()
	if err = os.RemoveAll(source.dataDir); err != nil {
		t.Fatal(err)
	}
	w := recoverNewRequest(target, archive.Body.Bytes(), code, origin)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), `"members":3`) {
		t.Fatal("automatic session or wrong members", w.Body.String())
	}
	if _, err = os.Stat(filepath.Join(target.dataDir, "setup-code")); !os.IsNotExist(err) {
		t.Fatal("setup code not consumed")
	}
	if w := recoverNewRequest(target, archive.Body.Bytes(), code, origin); w.Code != 409 {
		t.Fatal("recovery reused")
	}
	for _, table := range []string{"identity_sessions", "identity_invitations", "identity_recovery", "identity_mail_recovery", "identity_email_verification", "identity_verified_emails"} {
		var count int
		if err = target.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("stale identity secret or verified email restored", table, count, err)
		}
	}
	for _, user := range []string{"retired", "neighbor"} {
		var count int
		if err = target.db.QueryRow("SELECT count(*) FROM identity_users WHERE id=?", user).Scan(&count); err != nil || count != 0 {
			t.Fatal("inactive or foreign account restored", user)
		}
	}
	if w := perform(target.app, "GET", "/api/documents", nil, source.cookie); w.Code != 401 {
		t.Fatal("old owner session revived")
	}
	if w := perform(target.app, "GET", "/api/documents", nil, reader); w.Code != 401 {
		t.Fatal("old member session revived")
	}
	ownerCookie := loginRecovered(t, target, "alice@example.test", "a long private test passphrase")
	readerCookie := loginRecovered(t, target, "reader@example.test", "a long private test passphrase")
	adminCookie := loginRecovered(t, target, "admin@example.test", "a long private test passphrase")
	var newMemberID string
	if err = target.db.QueryRow("SELECT id FROM identity_member_ids WHERE user_id='reader'").Scan(&newMemberID); err != nil || newMemberID == oldMemberID {
		t.Fatal("membership identity reused")
	}
	if w := perform(target.app, "GET", "/api/documents", nil, ownerCookie); w.Code != 404 {
		t.Fatal("disabled module not preserved")
	}
	if w := perform(target.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": true}, ownerCookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	for _, cookie := range []*http.Cookie{ownerCookie, readerCookie} {
		w := perform(target.app, "GET", "/api/documents/"+d.ID+"/download", nil, cookie)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
			t.Fatal("original/grant not recovered", w.Code, w.Body.String())
		}
	}
	if w := perform(target.app, "GET", "/api/documents/"+stale.ID, nil, readerCookie); w.Code != 404 {
		t.Fatal("stale membership grant revived")
	}
	if w := perform(target.app, "GET", "/api/documents/"+private.ID, nil, ownerCookie); w.Code != 404 {
		t.Fatal("owner gained private browsing")
	}
	for _, cookie := range []*http.Cookie{readerCookie, ownerCookie} {
		if w := uploadRequest(t, target, "denied.txt", []byte("No write permission"), validMetadata, cookie); w.Code != 403 {
			t.Fatal("write permission enlarged", w.Code)
		}
	}
	if w := backupRequest(target, adminCookie); w.Code != 403 {
		t.Fatal("admin gained backup authority")
	}
	if w := perform(target.app, "GET", "/api/search?q=electrodomésticos", nil, readerCookie); w.Code != 200 || !strings.Contains(w.Body.String(), d.ID) || strings.Contains(w.Body.String(), stale.ID) {
		t.Fatal("recovered FTS violates permissions", w.Body.String())
	}
	if w := perform(target.app, "POST", "/api/auth/recover", map[string]string{"code": oldRecovery, "password": "a different recovery phrase"}, nil); w.Code != 410 {
		t.Fatal("old recovery token revived")
	}
	// Reopen the actual DB and Roots, rather than reusing the live connection.
	target.app.Close()
	target.db.Close()
	db, err := database.Open(filepath.Join(target.dataDir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := app.New(context.Background(), db, target.assets, origin, target.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	target.app = a
	target.db = db
	if w := uploadRequest(t, target, "denied.txt", []byte("Still no write"), validMetadata, ownerCookie); w.Code != 403 {
		t.Fatal("restart added owner capabilities")
	}
	if w := perform(target.app, "GET", "/api/documents/"+d.ID+"/download", nil, readerCookie); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("restore lost after full restart")
	}
	if _, _, err := identity.IssueRecovery(context.Background(), db, "alice@example.test"); err != nil {
		t.Fatal("local recovery unavailable after disaster restore", err)
	}
}

func TestFreshRecoveryRejectsInvalidCodeLegacyArchiveAndRollsBack(t *testing.T) {
	source := newFixture(t)
	decodeDocument(t, uploadRequest(t, source, "a.txt", []byte("Transactional recovery"), validMetadata, source.cookie))
	archive := backupRequest(source, source.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	target, code := emptyRecoveryTarget(t, source)
	if w := recoverNewRequest(target, archive.Body.Bytes(), "invalid", origin); w.Code != 403 {
		t.Fatal("code not required", w.Code)
	}
	assertEmptyRecovery(t, target)
	if w := recoverNewRequest(target, archive.Body.Bytes(), code, "https://evil.example.test"); w.Code != 403 {
		t.Fatal("cross origin allowed")
	}
	assertEmptyRecovery(t, target)
	legacyEntries := archiveEntries(t, archive.Body.Bytes())
	var legacyManifest map[string]any
	if err := json.Unmarshal(legacyEntries["manifest.json"], &legacyManifest); err != nil {
		t.Fatal(err)
	}
	legacyManifest["version"] = 1
	// A v1 snapshot contains the four data tables and no members table.
	legacyDB := filepath.Join(t.TempDir(), "legacy.sqlite")
	if err := os.WriteFile(legacyDB, legacyEntries["household.sqlite"], 0600); err != nil {
		t.Fatal(err)
	}
	legacySQL, err := sql.Open("sqlite", legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacySQL.Exec("DROP TABLE members"); err != nil {
		t.Fatal(err)
	}
	legacySQL.Close()
	legacyEntries["household.sqlite"], err = os.ReadFile(legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(legacyEntries["household.sqlite"])
	legacyManifest["sha256"].(map[string]any)["household.sqlite"] = hex.EncodeToString(digest[:])
	legacyEntries["manifest.json"], _ = json.Marshal(legacyManifest)
	legacy := httptest.NewRecorder()
	legacy.WriteHeader(200)
	legacy.Write(zipEntries(t, legacyEntries))
	if legacy.Code != 200 {
		t.Fatal(legacy.Code)
	}
	if w := restoreRequest(source, legacy.Body.Bytes(), source.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal("v1 existing-household restoration broken", w.Code, w.Body.String())
	}
	if w := recoverNewRequest(target, legacy.Body.Bytes(), code, origin); w.Code != 400 || !strings.Contains(w.Body.String(), "backup_identity_required") {
		t.Fatal("legacy archive initialized identity", w.Code, w.Body.String())
	}
	assertEmptyRecovery(t, target)
	if _, err := target.db.Exec("CREATE TRIGGER reject_import BEFORE INSERT ON documents BEGIN SELECT RAISE(ABORT,'failed restore'); END"); err != nil {
		t.Fatal(err)
	}
	if w := recoverNewRequest(target, archive.Body.Bytes(), code, origin); w.Code != 503 {
		t.Fatal("failed SQL not reported", w.Code, w.Body.String())
	}
	assertEmptyRecovery(t, target)
	if _, err := target.db.Exec("DROP TRIGGER reject_import"); err != nil {
		t.Fatal(err)
	}
	if w := recoverNewRequest(target, archive.Body.Bytes(), code, origin); w.Code != 201 {
		t.Fatal("retry after rollback failed", w.Code, w.Body.String())
	}
	if w := recoverNewRequest(source, archive.Body.Bytes(), code, origin); w.Code != 409 {
		t.Fatal("nonempty installation overwritten")
	}
}

func TestInstallationRecoverySharesSetupRateLimit(t *testing.T) {
	source := newFixture(t)
	target, code := emptyRecoveryTarget(t, source)
	for range 10 {
		if w := recoverNewRequest(target, nil, "invalid", origin); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if w := recoverNewRequest(target, nil, code, origin); w.Code != 429 {
		t.Fatal("recovery code brute force not limited", w.Code)
	}
	assertEmptyRecovery(t, target)
}

func TestCompleteRecoveryRejectsUntrustedMemberCredentialsAndGrants(t *testing.T) {
	source := newFixture(t)
	otherMember(t, source, "reader", source.state.Session.Household.ID)
	decodeDocument(t, uploadRequest(t, source, "a.txt", []byte("Integrity before identity import"), validMetadata, source.cookie))
	archive := backupRequest(source, source.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	for _, statement := range []string{`UPDATE members SET role='owner'`, `UPDATE members SET password_hash='plaintext-is-not-a-password-hash'`, `UPDATE members SET permissions='["documents.read","system.root"]'`} {
		t.Run(statement, func(t *testing.T) {
			entries := archiveEntries(t, archive.Body.Bytes())
			path := filepath.Join(t.TempDir(), "snapshot.sqlite")
			if err := os.WriteFile(path, entries["household.sqlite"], 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			db.Close()
			entries["household.sqlite"], err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var manifest map[string]any
			if err = json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(entries["household.sqlite"])
			manifest["sha256"].(map[string]any)["household.sqlite"] = hex.EncodeToString(digest[:])
			entries["manifest.json"], err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			target, code := emptyRecoveryTarget(t, source)
			if w := recoverNewRequest(target, zipEntries(t, entries), code, origin); w.Code != 400 {
				t.Fatal("untrusted identity imported", w.Code, w.Body.String())
			}
			assertEmptyRecovery(t, target)
		})
	}
}

func TestRecoveryPreservesNoEmailAccountsAndRequiresNewPINBindings(t *testing.T) {
	source := newFixture(t)
	w := perform(source.app, "PATCH", "/api/auth/access", map[string]string{"username": "family-owner", "password": "a long private test passphrase"}, source.cookie)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := source.db.Exec("UPDATE identity_users SET email=NULL WHERE id=?", source.state.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	var user, token string
	for _, name := range []string{"maria", "pedro"} {
		w = perform(source.app, "POST", "/api/household/assisted-members", map[string]any{"name": name, "username": name, "role": "restricted", "permissions": []string{"documents.read"}, "password": "a long private test passphrase"}, source.cookie)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		var link struct {
			URL    string
			UserID string `json:"userId"`
		}
		json.Unmarshal(w.Body.Bytes(), &link)
		if name == "maria" {
			user = link.UserID
			token = strings.Split(link.URL, "#")[1]
		}
	}
	w = perform(source.app, "POST", "/api/assisted/complete", map[string]string{"token": token, "pin": "642817", "deviceName": "Mi tablet"}, nil)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	oldDevice := w.Result().Cookies()[0]
	d := decodeDocument(t, uploadRequest(t, source, "family.txt", []byte("Documento familiar compartido"), validMetadata, source.cookie))
	if w = perform(source.app, "PUT", "/api/documents/"+d.ID+"/shares/"+user, nil, source.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	archive := backupRequest(source, source.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	var manifest map[string]any
	json.Unmarshal(archiveEntries(t, archive.Body.Bytes())["manifest.json"], &manifest)
	if manifest["version"] != float64(3) {
		t.Fatal("new identities were not preserved with v3", manifest)
	}
	target, setupCode := emptyRecoveryTarget(t, source)
	if w = recoverNewRequest(target, archive.Body.Bytes(), setupCode, origin); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	if err := target.db.QueryRow("SELECT count(*) FROM identity_users WHERE email IS NULL AND assisted=1 AND username IN ('maria','pedro')").Scan(&count); err != nil || count != 2 {
		t.Fatal("email-less identities lost", count, err)
	}
	for _, table := range []string{"identity_pin_devices", "identity_assisted_enrollments", "identity_sessions"} {
		if err := target.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("old authorization restored", table, count, err)
		}
	}
	if w = perform(target.app, "POST", "/api/auth/pin/login", map[string]string{"pin": "642817"}, oldDevice); w.Code != 401 {
		t.Fatal("old device authorized on new installation")
	}
	owner := loginRecovered(t, target, "family-owner", "a long private test passphrase")
	w = perform(target.app, "POST", "/api/household/members/"+user+"/enrollment", map[string]string{"password": "a long private test passphrase"}, owner)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var link struct{ URL string }
	json.Unmarshal(w.Body.Bytes(), &link)
	w = perform(target.app, "POST", "/api/assisted/complete", map[string]string{"token": strings.Split(link.URL, "#")[1], "pin": "728416", "deviceName": "Nuevo equipo"}, nil)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = perform(target.app, "POST", "/api/auth/pin/login", map[string]string{"pin": "728416"}, w.Result().Cookies()[0])
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if w = perform(target.app, "GET", "/api/documents/"+d.ID+"/download", nil, cookie); w.Code != 200 || w.Body.String() != "Documento familiar compartido" {
		t.Fatal("restored document grant lost", w.Code, w.Body.String())
	}
	if w = perform(target.app, "GET", "/api/documents/private", nil, cookie); w.Code == 200 {
		t.Fatal("assisted recovery disclosed private document")
	}
}
