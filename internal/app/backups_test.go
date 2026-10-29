package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/home-atlas/homeatlas/internal/app"
)

const testBackupPassword = "Copia segura de prueba 2026"

func backupRequest(f fixture, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", origin+"/api/household/backups", nil)
	r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(testBackupPassword)))
	r.Header.Set("Origin", origin)
	r.Header.Set("X-HomeAtlas-Backup-Version", "2")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, r)
	return w
}
func restoreRequest(f fixture, data []byte, cookie *http.Cookie, originHeader, confirm string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", origin+"/api/household/restore", bytes.NewReader(data))
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		r.Header.Set("X-HomeAtlas-Allow-Legacy-Backup", "true")
	}
	r.Header.Set("Content-Type", "application/zip")
	r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(testBackupPassword)))
	r.Header.Set("Origin", originHeader)
	r.Header.Set("X-HomeAtlas-Confirm-Restore", confirm)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, r)
	return w
}
func archiveEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	if bytes.HasPrefix(data, []byte("age-encryption.org/v1\n")) {
		identity, err := age.NewScryptIdentity(testBackupPassword)
		if err != nil {
			t.Fatal(err)
		}
		identity.SetMaxWorkFactor(18)
		reader, err := age.Decrypt(bytes.NewReader(data), identity)
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name], err = io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func zipEntries(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	for name, body := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestHouseholdBackupRoundTripPreservesLiveIdentityAndIsolation(t *testing.T) {
	f := newFixture(t)
	member := otherMember(t, f, "reader", f.state.Session.Household.ID)
	other := otherMember(t, f, "neighbor", "neighbor-home")
	original := []byte("Original de prueba, con contenido privado.\n")
	d := decodeDocument(t, uploadRequest(t, f, "private.txt", original, validMetadata, f.cookie))
	private := decodeDocument(t, uploadRequest(t, f, "member.txt", []byte("Solo del miembro"), validMetadata, member))
	if w := perform(f.app, "PUT", "/api/documents/"+d.ID+"/shares/reader", nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": false}, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	backup := backupRequest(f, f.cookie)
	if backup.Code != 200 {
		t.Fatal(backup.Code, backup.Body.String())
	}
	if backup.Header().Get("Content-Type") != "application/octet-stream" || backup.Header().Get("Cache-Control") != "no-store" || !strings.Contains(backup.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("unsafe backup response")
	}
	entries := archiveEntries(t, backup.Body.Bytes())
	if len(entries) != 4 {
		t.Fatal("unexpected archive entries", len(entries))
	}
	path := filepath.Join(t.TempDir(), "household.sqlite")
	if err := os.WriteFile(path, entries["household.sqlite"], 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow("SELECT count(*) FROM documents").Scan(&count); err != nil || count != 4 {
		t.Fatal("missing household snapshot", count, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'identity_%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("credentials exported")
	}
	if err = db.QueryRow("SELECT count(*) FROM documents WHERE id='other-home'").Scan(&count); err != nil || count != 0 {
		t.Fatal("other household exported")
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": true}, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	meta := map[string]any{"title": "Título nuevo", "category": "otros", "description": "posterior", "tags": []string{}}
	if w := perform(f.app, "PATCH", "/api/documents/"+d.ID, meta, f.cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	newDoc := decodeDocument(t, uploadRequest(t, f, "new.txt", []byte("Documento posterior a la copia"), validMetadata, f.cookie))
	if w := perform(f.app, "PATCH", "/api/household/settings", map[string]string{"name": "Casa nueva", "timezone": "UTC"}, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := restoreRequest(f, backup.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if currentModules(t, f, f.cookie)[0].Enabled {
		t.Fatal("module preference not restored")
	}
	if w := perform(f.app, "GET", "/api/auth/session", nil, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), "Home A") {
		t.Fatal("session or household not preserved", w.Body.String())
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": true}, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	for _, cookie := range []*http.Cookie{f.cookie, member} {
		w := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, cookie)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), original) {
			t.Fatal("file/grant not restored", w.Code, w.Body.String())
		}
	}
	if w := perform(f.app, "GET", "/api/documents/"+private.ID, nil, f.cookie); w.Code != 404 {
		t.Fatal("backup permission enabled private browsing")
	}
	if w := perform(f.app, "GET", "/api/documents/"+newDoc.ID, nil, f.cookie); w.Code != 404 {
		t.Fatal("new document survived replace")
	}
	if w := perform(f.app, "GET", "/api/documents", nil, other); w.Code != 200 {
		t.Fatal("other household affected")
	}
	if w := perform(f.app, "GET", "/api/search?q=electrodomésticos", nil, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), d.ID) {
		t.Fatal("FTS not rebuilt", w.Body.String())
	}
	previous := perform(f.app, "GET", "/api/household/backups/previous", nil, f.cookie)
	if previous.Code != 200 {
		t.Fatal(previous.Code)
	}
	if w := restoreRequest(f, previous.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal("undo failed", w.Code, w.Body.String())
	}
	if w := perform(f.app, "GET", "/api/documents/"+newDoc.ID, nil, f.cookie); w.Code != 200 {
		t.Fatal("undo did not restore later document")
	}
	restarted, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if w := perform(restarted, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), original) {
		t.Fatal("restore lost on restart")
	}
}

func TestBackupOwnerPermissionCSRFAndRevocation(t *testing.T) {
	f := newFixture(t)
	member := otherMember(t, f, "admin", f.state.Session.Household.ID)
	if _, err := f.db.Exec("UPDATE identity_memberships SET role='admin' WHERE user_id='admin'; INSERT INTO identity_permissions SELECT 'admin',household_id,'household.manage' FROM identity_memberships WHERE user_id='admin'; INSERT INTO identity_permissions SELECT 'admin',household_id,'household.backup' FROM identity_memberships WHERE user_id='admin'"); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range []*http.Cookie{nil, member} {
		want := 403
		if cookie == nil {
			want = 401
		}
		if w := backupRequest(f, cookie); w.Code != want {
			t.Fatal("backup authorization", w.Code)
		}
		if w := restoreRequest(f, []byte("bad"), cookie, origin, "replace-household"); w.Code != want {
			t.Fatal("restore authorization", w.Code)
		}
		if w := perform(f.app, "GET", "/api/household/backups/previous", nil, cookie); w.Code != want {
			t.Fatal("previous authorization", w.Code)
		}
	}
	backup := backupRequest(f, f.cookie)
	if backup.Code != 200 {
		t.Fatal(backup.Code, backup.Body.String())
	}
	if w := restoreRequest(f, backup.Body.Bytes(), f.cookie, "https://evil.example.test", "replace-household"); w.Code != 403 {
		t.Fatal("CSRF restore allowed")
	}
	if w := restoreRequest(f, backup.Body.Bytes(), f.cookie, origin, ""); w.Code != 400 {
		t.Fatal("missing confirmation allowed")
	}
	if _, err := f.db.Exec("DELETE FROM identity_permissions WHERE capability='household.backup'"); err != nil {
		t.Fatal(err)
	}
	if w := backupRequest(f, f.cookie); w.Code != 403 {
		t.Fatal("revoked backup allowed")
	}
	restarted, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if w := perform(restarted, "POST", "/api/household/backups", nil, f.cookie); w.Code != 403 {
		t.Fatal("restart regranted backup permission")
	}
}

func TestRestoreRejectsCorruptionTraversalForeignHouseholdAndRollsBack(t *testing.T) {
	f := newFixture(t)
	d := decodeDocument(t, uploadRequest(t, f, "keep.txt", []byte("Preservar el original"), validMetadata, f.cookie))
	original := backupRequest(f, f.cookie)
	if original.Code != 200 {
		t.Fatal(original.Code, original.Body.String())
	}
	for _, kind := range []string{"truncated", "checksum", "traversal", "foreign", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			entries := archiveEntries(t, original.Body.Bytes())
			want := 400
			switch kind {
			case "checksum":
				entries["household.sqlite"][30] ^= 1
			case "traversal":
				entries["../escape"] = []byte("no")
			case "unknown":
				entries["surprise"] = []byte("no")
			case "foreign":
				var manifest map[string]any
				json.Unmarshal(entries["manifest.json"], &manifest)
				manifest["householdId"] = "another-home"
				entries["manifest.json"], _ = json.Marshal(manifest)
				want = 409
			}
			data := zipEntries(t, entries)
			if kind == "truncated" {
				data = data[:len(data)/2]
			}
			if w := restoreRequest(f, data, f.cookie, origin, "replace-household"); w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie); w.Code != 200 || w.Body.String() != "Preservar el original" {
				t.Fatal("bad restore changed data")
			}
		})
	}
	before, _ := os.ReadDir(filepath.Join(f.dataDir, "documents"))
	if _, err := f.db.Exec("CREATE TRIGGER fail_restore BEFORE INSERT ON documents BEGIN SELECT RAISE(ABORT,'forced restore failure'); END"); err != nil {
		t.Fatal(err)
	}
	if w := restoreRequest(f, original.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 503 {
		t.Fatal("SQL failure ignored", w.Code, w.Body.String())
	}
	after, _ := os.ReadDir(filepath.Join(f.dataDir, "documents"))
	if len(after) != len(before) {
		t.Fatal("failed restore leaked staged originals")
	}
	if w := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie); w.Code != 200 {
		t.Fatal("SQL rollback lost original")
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "escape")); !os.IsNotExist(err) {
		t.Fatal("escaped extraction root")
	}
}

func TestRestoreDoesNotResurrectRevokedSharesOrIdentity(t *testing.T) {
	f := newFixture(t)
	member := otherMember(t, f, "reader", f.state.Session.Household.ID)
	d := decodeDocument(t, uploadRequest(t, f, "a.txt", []byte("Revoked access stays revoked"), validMetadata, f.cookie))
	base := "/api/documents/" + d.ID
	if w := perform(f.app, "PUT", base+"/shares/reader", nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	backup := backupRequest(f, f.cookie)
	if backup.Code != 200 {
		t.Fatal(backup.Code)
	}
	if w := perform(f.app, "DELETE", base+"/shares/reader", nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, err := f.db.Exec("UPDATE identity_users SET password_hash='changed-after-backup' WHERE id=?", f.state.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	if w := restoreRequest(f, backup.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := perform(f.app, "GET", base+"/download", nil, member); w.Code != 404 {
		t.Fatal("revoked grant restored")
	}
	var hash string
	if err := f.db.QueryRow("SELECT password_hash FROM identity_users WHERE id=?", f.state.Session.User.ID).Scan(&hash); err != nil || hash != "changed-after-backup" {
		t.Fatal("live authentication rolled back")
	}
}

func TestRestoreRejectsUntrustedSQLiteAndCrossHouseholdDocumentIDs(t *testing.T) {
	f := newFixture(t)
	backup := backupRequest(f, f.cookie)
	if backup.Code != 200 {
		t.Fatal(backup.Code, backup.Body.String())
	}
	for _, operation := range []string{"CREATE VIEW extra AS SELECT * FROM documents", "UPDATE documents SET id='other-home' WHERE id='mine'", "INSERT INTO household VALUES('extra','Extra','UTC')"} {
		t.Run(operation, func(t *testing.T) {
			entries := archiveEntries(t, backup.Body.Bytes())
			path := filepath.Join(t.TempDir(), "snapshot.sqlite")
			if err := os.WriteFile(path, entries["household.sqlite"], 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(operation); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			entries["household.sqlite"], err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var manifest struct {
				Format      string            `json:"format"`
				Version     int               `json:"version"`
				HouseholdID string            `json:"householdId"`
				CreatedAt   string            `json:"createdAt"`
				SHA256      map[string]string `json:"sha256"`
			}
			if err = json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(entries["household.sqlite"])
			manifest.SHA256["household.sqlite"] = hex.EncodeToString(digest[:])
			entries["manifest.json"], _ = json.Marshal(manifest)
			want := 400
			if strings.HasPrefix(operation, "UPDATE documents") {
				want = 409
			}
			if w := restoreRequest(f, zipEntries(t, entries), f.cookie, origin, "replace-household"); w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if w := perform(f.app, "GET", "/api/documents/mine", nil, f.cookie); w.Code != 200 {
				t.Fatal("invalid restore changed live data")
			}
		})
	}
}

func TestBackupRefusesMissingOrCorruptedOriginals(t *testing.T) {
	f := newFixture(t)
	d := decodeDocument(t, uploadRequest(t, f, "a.txt", []byte("Intact original"), validMetadata, f.cookie))
	var key string
	if err := f.db.QueryRow("SELECT file_key FROM documents WHERE id=?", d.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dataDir, "documents", key), []byte("Wrong original"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := backupRequest(f, f.cookie); w.Code == 200 {
		t.Fatal("corrupt original exported as valid backup")
	}
	if err := os.Remove(filepath.Join(f.dataDir, "documents", key)); err != nil {
		t.Fatal(err)
	}
	if w := backupRequest(f, f.cookie); w.Code == 200 {
		t.Fatal("missing original exported as valid backup")
	}
}

func TestRestoreRejectsOversizeAndDuplicateEntries(t *testing.T) {
	f := newFixture(t)
	r := httptest.NewRequest("POST", origin+"/api/household/restore", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/zip")
	r.Header.Set("Origin", origin)
	r.Header.Set("X-HomeAtlas-Confirm-Restore", "replace-household")
	r.AddCookie(f.cookie)
	r.ContentLength = (1 << 30) + 1
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal("oversized upload not rejected", w.Code)
	}
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for range 2 {
		entry, err := writer.Create("manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte("{}"))
	}
	writer.Close()
	if w := restoreRequest(f, data.Bytes(), f.cookie, origin, "replace-household"); w.Code != 400 {
		t.Fatal("duplicate entries accepted", w.Code)
	}
}

func TestRestoreCanRecoverMissingOrDamagedLiveOriginals(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing", false: "damaged"}[missing], func(t *testing.T) {
			f := newFixture(t)
			original := []byte("Original íntegro para recuperar")
			d := decodeDocument(t, uploadRequest(t, f, "a.txt", original, validMetadata, f.cookie))
			backup := backupRequest(f, f.cookie)
			if backup.Code != 200 {
				t.Fatal(backup.Code)
			}
			var key string
			if err := f.db.QueryRow("SELECT file_key FROM documents WHERE id=?", d.ID).Scan(&key); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.dataDir, "documents", key)
			if missing {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte("Damaged"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			w := restoreRequest(f, backup.Body.Bytes(), f.cookie, origin, "replace-household")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"previousUnavailableOriginals":1`) {
				t.Fatal("recovery blocked by missing live original", w.Code, w.Body.String())
			}
			if w := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), original) {
				t.Fatal("original not recovered")
			}
			previous := perform(f.app, "GET", "/api/household/backups/previous", nil, f.cookie)
			if previous.Code != 200 {
				t.Fatal(previous.Code)
			}
			entries := archiveEntries(t, previous.Body.Bytes())
			if !strings.Contains(string(entries["manifest.json"]), `"unavailableOriginals"`) {
				t.Fatal("degraded safety copy not declared")
			}
			w = restoreRequest(f, previous.Body.Bytes(), f.cookie, origin, "replace-household")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"unavailableOriginals":1`) {
				t.Fatal("degraded backup cannot restore metadata", w.Code, w.Body.String())
			}
			if w := perform(f.app, "GET", "/api/documents/"+d.ID, nil, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"hasFile":false`) {
				t.Fatal("unavailable file misrepresented", w.Code, w.Body.String())
			}
			if w := backupRequest(f, f.cookie); w.Code != 409 || !strings.Contains(w.Body.String(), "backup_original_unavailable") {
				t.Fatal("degraded state exported as a complete backup", w.Code, w.Body.String())
			}
		})
	}
}
