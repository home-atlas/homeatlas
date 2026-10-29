package app_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func encryptedRequest(f fixture, method, route string, data []byte, passwordHeader, setupCode string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, origin+route, bytes.NewReader(data))
	r.Header.Set("Origin", origin)
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		r.Header.Set("X-HomeAtlas-Allow-Legacy-Backup", "true")
	}
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-HomeAtlas-Backup-Version", "2")
	r.Header.Set("X-HomeAtlas-Backup-Password", passwordHeader)
	if setupCode != "" {
		r.Header.Set("X-HomeAtlas-Setup-Code", setupCode)
		r.Header.Set("X-HomeAtlas-Confirm-Restore", "initialize-installation")
	} else {
		r.Header.Set("X-HomeAtlas-Confirm-Restore", "replace-household")
		if f.cookie != nil {
			r.AddCookie(f.cookie)
		}
	}
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, r)
	return w
}
func passwordEncoded(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
func assertNoBackupTemps(t *testing.T, f fixture) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(f.dataDir, ".household-backup-*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("backup left plaintext temporary files", matches, err)
	}
}
func TestBackupRequiresPasswordAndAcceptsExactUnicodePassphrase(t *testing.T) {
	f := newFixture(t)
	for _, value := range []string{"", "not base64!", passwordEncoded("corta"), passwordEncoded(strings.Repeat("á", 129)), passwordEncoded(strings.Repeat(" ", 12)), passwordEncoded(string([]byte{0xff}))} {
		w := encryptedRequest(f, "POST", "/api/household/backups", nil, value, "")
		if w.Code != 400 || !strings.Contains(w.Body.String(), "password") {
			t.Fatal("invalid password accepted", w.Code)
		}
	}
	password := "  Árbol y café, hogar 2026  "
	w := encryptedRequest(f, "POST", "/api/household/backups", nil, passwordEncoded(password), "")
	if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("age-encryption.org/v1\n")) {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), ".zip.age") {
		t.Fatal("missing encrypted filename")
	}
	identity, _ := age.NewScryptIdentity(password)
	identity.SetMaxWorkFactor(18)
	reader, err := age.Decrypt(bytes.NewReader(w.Body.Bytes()), identity)
	if err != nil {
		t.Fatal("not interoperable with standard age", err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(archiveEntries(t, plain)) != 2 {
		t.Fatal("incorrect encrypted snapshot")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("household.sqlite")) {
		t.Fatal("metadata leaked")
	}
	assertNoBackupTemps(t, f)
}
func TestEncryptedRestoreAuthenticatesAllBytesBeforeEitherImport(t *testing.T) {
	f := newFixture(t)
	doc := decodeDocument(t, uploadRequest(t, f, "private.txt", []byte("Contenido antes de restaurar"), validMetadata, f.cookie))
	archive := backupRequest(f, f.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	correct := passwordEncoded(testBackupPassword)
	tampered := bytes.Clone(archive.Body.Bytes())
	tampered[len(tampered)-1] ^= 1
	expensive := bytes.Replace(archive.Body.Bytes(), []byte(" 18\n"), []byte(" 30\n"), 1)
	cases := []struct {
		name   string
		data   []byte
		header string
	}{
		{"missing-password", archive.Body.Bytes(), ""},
		{"wrong-password", archive.Body.Bytes(), passwordEncoded("Contraseña diferente 2026")},
		{"tampered-final-tag", tampered, correct},
		{"truncated", archive.Body.Bytes()[:len(archive.Body.Bytes())-1], correct},
		{"appended", append(bytes.Clone(archive.Body.Bytes()), 'x'), correct},
		{"excessive-kdf", expensive, correct},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := encryptedRequest(f, "POST", "/api/household/restore", c.data, c.header, "")
			if w.Code != 400 {
				t.Fatal("invalid encrypted archive accepted", w.Code, w.Body.String())
			}
			if w := perform(f.app, "GET", "/api/documents/"+doc.ID+"/download", nil, f.cookie); w.Code != 200 || w.Body.String() != "Contenido antes de restaurar" {
				t.Fatal("failed decrypt changed live data")
			}
			if w := perform(f.app, "GET", "/api/household/backups/previous", nil, f.cookie); w.Code != http.StatusNotFound {
				t.Fatal("failed decrypt replaced safety copy")
			}
			assertNoBackupTemps(t, f)
			target, code := emptyRecoveryTarget(t, f)
			w = encryptedRequest(target, "POST", "/api/setup/restore", c.data, c.header, code)
			if w.Code != 400 {
				t.Fatal("invalid encrypted archive initialized server", w.Code, w.Body.String())
			}
			assertEmptyRecovery(t, target)
			assertNoBackupTemps(t, target)
		})
	}
}
func TestLegacyArchivesRestoreAndSafetyCopiesAreAlwaysEncrypted(t *testing.T) {
	f := newFixture(t)
	archive := backupRequest(f, f.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code)
	}
	plain := zipEntries(t, archiveEntries(t, archive.Body.Bytes()))
	// Historical full v2 ZIP still initializes a new server without an encryption password.
	target, code := emptyRecoveryTarget(t, f)
	if w := encryptedRequest(target, "POST", "/api/setup/restore", plain, "", code); w.Code != 201 {
		t.Fatal("historical v2 recovery broken", w.Code, w.Body.String())
	}
	// Existing-home restore needs a password even for a historical unencrypted ZIP.
	if w := encryptedRequest(f, "POST", "/api/household/restore", plain, "", ""); w.Code != 400 {
		t.Fatal("unencrypted safety copy allowed")
	}
	if w := restoreRequest(f, plain, f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	path := filepath.Join(f.dataDir, "backups", f.state.Session.Household.ID+"-before-restore.zip")
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(stored, []byte("age-encryption.org/v1\n")) {
		t.Fatal("safety copy at rest is not encrypted", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe safety copy permissions", err)
	}
	previous := perform(f.app, "GET", "/api/household/backups/previous", nil, f.cookie)
	if previous.Code != 200 || !bytes.Equal(previous.Body.Bytes(), stored) {
		t.Fatal("encrypted safety copy changed during download")
	}
	archiveEntries(t, stored) // Same password used to restore decrypts the safety copy with standard age.
	// A retained safety copy from an older app is never downloaded in plaintext.
	if err := os.WriteFile(path, plain, 0600); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "GET", "/api/household/backups/previous", nil, f.cookie); w.Code != 400 {
		t.Fatal("legacy safety copy downloaded without encryption")
	}
	previous = encryptedRequest(f, "GET", "/api/household/backups/previous", nil, passwordEncoded(testBackupPassword), "")
	if previous.Code != 200 || !bytes.HasPrefix(previous.Body.Bytes(), []byte("age-encryption.org/v1\n")) {
		t.Fatal("legacy safety copy download not encrypted", previous.Code)
	}
	archiveEntries(t, previous.Body.Bytes())
	assertNoBackupTemps(t, f)
}

func TestEncryptedImportCannotSilentlyDowngradeToHistoricalZIP(t *testing.T) {
	f := newFixture(t)
	archive := backupRequest(f, f.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code)
	}
	plain := zipEntries(t, archiveEntries(t, archive.Body.Bytes()))
	target, code := emptyRecoveryTarget(t, f)
	for _, scenario := range []struct {
		f                    fixture
		route, code, confirm string
	}{
		{f, "/api/household/restore", "", "replace-household"},
		{target, "/api/setup/restore", code, "initialize-installation"},
	} {
		r := httptest.NewRequest("POST", origin+scenario.route, bytes.NewReader(plain))
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-HomeAtlas-Backup-Password", passwordEncoded(testBackupPassword))
		r.Header.Set("X-HomeAtlas-Confirm-Restore", scenario.confirm)
		r.Header.Set("X-HomeAtlas-Setup-Code", scenario.code)
		if scenario.f.cookie != nil {
			r.AddCookie(scenario.f.cookie)
		}
		w := httptest.NewRecorder()
		scenario.f.app.Handler.ServeHTTP(w, r)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "backup_decryption_failed") {
			t.Fatal("encrypted import silently accepted plaintext", w.Code, w.Body.String())
		}
	}
	assertEmptyRecovery(t, target)
}
