//go:build !race

package app_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/plugins"
)

func documentsPackage(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	wasm := filepath.Join(dir, "documents.wasm")
	archive := filepath.Join(dir, "documents.hap")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", wasm, "./plugins/documents")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile guest: %v %s", err, out)
	}
	cmd = exec.Command("python3", "scripts/package-plugin.py", "plugins/documents/manifest.json", wasm, archive)
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package guest: %v %s", err, out)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	return archive, fmt.Sprintf("%x", sha256.Sum256(raw))
}
func installDocuments(t *testing.T, f *fixture, archive, digest string) {
	t.Helper()
	f.app.Close()
	if _, err := plugins.Install(context.Background(), filepath.Join(f.dataDir, "plugins"), archive, digest, []string{"documents.read", "documents.write", "documents.share", "documents.files", "documents.collection"}); err != nil {
		t.Fatal(err)
	}
	reopenExternalDocuments(t, f)
}
func reopenExternalDocuments(t *testing.T, f *fixture) {
	t.Helper()
	a, err := app.NewWithOptions(context.Background(), f.db, f.assets, origin, f.dataDir, nil, app.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f.app = a
}
func TestPersistentPluginExternalDocuments(t *testing.T) {
	archive, digest := documentsPackage(t)
	f := newCoreFixture(t, true)
	installDocuments(t, &f, archive, digest)
	defer func() { f.app.Close() }()
	list := perform(f.app, "GET", "/api/modules", nil, f.cookie)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"runtime":"wasi-preview1"`) || !strings.Contains(list.Body.String(), `"collection"`) {
		t.Fatal(list.Code, list.Body.String())
	}
	largeTags := []string{}
	for i := 0; i < 10; i++ {
		largeTags = append(largeTags, strings.Repeat("<", 39)+fmt.Sprint(i))
	}
	escaped, _ := json.Marshal(map[string]any{"title": strings.Repeat("<", 160), "category": "otros", "description": strings.Repeat("<", 2000), "tags": largeTags})
	if w := uploadRequest(t, f, "escaped.txt", []byte("Escaped metadata"), string(escaped), f.cookie); w.Code != 201 {
		t.Fatal("valid escaped metadata", w.Code, w.Body.String())
	}
	doc := decodeDocument(t, uploadRequest(t, f, "manual.txt", []byte("Manual externo"), validMetadata, f.cookie))
	if w := perform(f.app, "GET", "/api/search?q=factura", nil, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), doc.ID) {
		t.Fatal(w.Code, w.Body.String())
	}
	reader, cookie := acceptMember(t, f, inviteMember(t, f, "external-reader@example.test", "member", []string{"documents.read", "documents.write", "documents.share"}, f.cookie))
	if w := perform(f.app, "GET", "/api/documents/"+doc.ID, nil, cookie); w.Code != 404 {
		t.Fatal("private leak", w.Code)
	}
	path := "/api/documents/" + doc.ID + "/shares/" + reader.Session.User.ID
	if w := perform(f.app, "PUT", path, nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := perform(f.app, "GET", "/api/documents/"+doc.ID+"/download", nil, cookie); w.Code != 200 || w.Body.String() != "Manual externo" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := perform(f.app, "PATCH", "/api/documents/"+doc.ID, map[string]any{"title": "forbidden", "category": "otros", "description": "", "tags": []string{}}, cookie); w.Code != 404 {
		t.Fatal("reader edited", w.Code)
	}
	copy := backupRequest(f, f.cookie)
	if copy.Code != 200 {
		t.Fatal(copy.Code, copy.Body.String())
	}
	if w := perform(f.app, "DELETE", path, nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := restoreRequest(f, copy.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := perform(f.app, "GET", "/api/documents/"+doc.ID, nil, cookie); w.Code != 404 {
		t.Fatal("restored revoked sharing", w.Code)
	}
	if w := perform(f.app, "GET", "/api/documents/"+doc.ID+"/download", nil, f.cookie); w.Code != 200 || w.Body.String() != "Manual externo" {
		t.Fatal(w.Code, w.Body.String())
	}
	f.app.Close()
	reopenExternalDocuments(t, &f)
	if w := perform(f.app, "GET", "/api/documents/"+doc.ID, nil, f.cookie); w.Code != 200 {
		t.Fatal("restart", w.Code, w.Body.String())
	}
	empty := newCoreFixture(t, false)
	installDocuments(t, &empty, archive, digest)
	defer func() { empty.app.Close() }()
	code, err := os.ReadFile(filepath.Join(empty.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	w := encryptedRequest(empty, "POST", "/api/setup/restore", copy.Body.Bytes(), base64.StdEncoding.EncodeToString([]byte(testBackupPassword)), strings.TrimSpace(string(code)))
	if w.Code != 201 {
		t.Fatal("recover", w.Code, w.Body.String())
	}
	w = perform(empty.app, "POST", "/api/auth/login", map[string]string{"identifier": "external-reader@example.test", "password": invitedPassword}, nil)
	if w.Code != 200 {
		t.Fatal("recovered reader", w.Code, w.Body.String())
	}
	var recoveredCookie *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if strings.Contains(cookie.Name, "session") {
			recoveredCookie = cookie
		}
	}
	if w := perform(empty.app, "GET", "/api/documents/"+doc.ID+"/download", nil, recoveredCookie); w.Code != 200 || w.Body.String() != "Manual externo" {
		t.Fatal("remapped grant", w.Code, w.Body.String())
	}
	var state struct {
		Session struct {
			Permissions []string `json:"permissions"`
		} `json:"session"`
	}
	if err := json.Unmarshal(perform(f.app, "GET", "/api/auth/session", nil, f.cookie).Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
}
func TestPersistentPluginDocumentsAdoptsExistingSchema(t *testing.T) {
	archive, digest := documentsPackage(t)
	f := newFixture(t)
	doc := decodeDocument(t, uploadRequest(t, f, "legacy.txt", []byte("Legacy preserved"), validMetadata, f.cookie))
	oldCopy := backupRequest(f, f.cookie)
	if oldCopy.Code != 200 {
		t.Fatal(oldCopy.Code, oldCopy.Body.String())
	}
	if _, err := f.db.Exec("DELETE FROM identity_permissions WHERE user_id=? AND capability='documents.write'", f.state.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	installDocuments(t, &f, archive, digest)
	defer func() { f.app.Close() }()
	if w := perform(f.app, "GET", "/api/documents/"+doc.ID+"/download", nil, f.cookie); w.Code != 200 || w.Body.String() != "Legacy preserved" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := uploadRequest(t, f, "revoked.txt", []byte("no"), validMetadata, f.cookie); w.Code != 403 {
		t.Fatal("revoked permission reapplied", w.Code, w.Body.String())
	}
	if w := restoreRequest(f, oldCopy.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal("historical copy", w.Code, w.Body.String())
	}
	if w := perform(f.app, "GET", "/api/documents/"+doc.ID+"/download", nil, f.cookie); w.Code != 200 || w.Body.String() != "Legacy preserved" {
		t.Fatal("historical original", w.Code, w.Body.String())
	}
}

func documentsVariant(t *testing.T, archive string, change func(map[string]any)) (string, string) {
	t.Helper()
	src, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	out := filepath.Join(t.TempDir(), "variant.hap")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(file)
	for _, entry := range src.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "manifest.json" {
			var m map[string]any
			if err := json.Unmarshal(body, &m); err != nil {
				t.Fatal(err)
			}
			change(m)
			body, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
		}
		w, err := z.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return out, fmt.Sprintf("%x", sha256.Sum256(raw))
}
func TestPersistentPluginCollectionIsolationAndPackagePin(t *testing.T) {
	archive, digest := documentsPackage(t)
	f := newCoreFixture(t, true)
	installDocuments(t, &f, archive, digest)
	defer func() { f.app.Close() }()
	doc := decodeDocument(t, uploadRequest(t, f, "first.txt", []byte("Only original collection"), validMetadata, f.cookie))
	copy := backupRequest(f, f.cookie)
	if copy.Code != 200 {
		t.Fatal(copy.Code, copy.Body.String())
	}
	other, otherDigest := documentsVariant(t, archive, func(m map[string]any) {
		m["id"] = "ext-files-fixture"
		m["name"] = "File fixture"
		c := m["collection"].(map[string]any)
		c["legacySnapshot"] = false
	})
	f.app.Close()
	if _, err := plugins.Install(context.Background(), filepath.Join(f.dataDir, "plugins"), other, otherDigest, []string{"ext-files-fixture.read", "ext-files-fixture.write", "ext-files-fixture.share", "ext-files-fixture.files", "ext-files-fixture.collection"}); err != nil {
		t.Fatal(err)
	}
	reopenExternalDocuments(t, &f)
	if w := perform(f.app, "GET", "/api/ext-files-fixture/"+doc.ID, nil, f.cookie); w.Code != 404 {
		t.Fatal("cross-collection access", w.Code, w.Body.String())
	}
	if w := perform(f.app, "GET", "/api/ext-files-fixture", nil, f.cookie); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := backupRequest(f, f.cookie); w.Code != 200 {
		t.Fatal("multiple collection providers", w.Code, w.Body.String())
	}
	wrong, wrongDigest := documentsVariant(t, archive, func(m map[string]any) { m["name"] = "Different approved package" })
	target := newCoreFixture(t, false)
	installDocuments(t, &target, wrong, wrongDigest)
	defer func() { target.app.Close() }()
	code, err := os.ReadFile(filepath.Join(target.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	w := encryptedRequest(target, "POST", "/api/setup/restore", copy.Body.Bytes(), base64.StdEncoding.EncodeToString([]byte(testBackupPassword)), strings.TrimSpace(string(code)))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "backup_module_required") {
		t.Fatal("different package accepted", w.Code, w.Body.String())
	}
	var count int
	if err := target.db.QueryRow("SELECT count(*) FROM identity_users").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed recovery changed identity", count, err)
	}
}
