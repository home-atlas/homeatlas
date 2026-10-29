package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/app/testdata/legacydocuments"
)

func uploadRequest(t *testing.T, f fixture, filename string, contents []byte, meta string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	field, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	field.Write(contents)
	writer.WriteField("metadata", meta)
	writer.Close()
	req := httptest.NewRequest("POST", origin+"/api/documents", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", origin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, req)
	return w
}

const validMetadata = `{"title":"Factura cocina","category":"recibos","description":"Compra de electrodomésticos","tags":["Cocina","cocina","2026"]}`

func decodeDocument(t *testing.T, w *httptest.ResponseRecorder) documents.Document {
	t.Helper()
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("document response: %d %s", w.Code, w.Body.String())
	}
	var d documents.Document
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}
func otherMember(t *testing.T, f fixture, user, household string) *http.Cookie {
	t.Helper()
	if _, err := f.db.Exec(`INSERT OR IGNORE INTO identity_households(id,name,timezone) VALUES(?,'Other','UTC')`, household); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO identity_users(id,name,email,password_hash) VALUES(?,?,?,'unused')`, []any{user, user, user + "@example.test"}},
		{`INSERT INTO identity_memberships VALUES(?,?,'member')`, []any{user, household}},
		{`INSERT INTO identity_permissions VALUES(?,?,'documents.read')`, []any{user, household}},
		{`INSERT INTO identity_permissions VALUES(?,?,'documents.write')`, []any{user, household}},
	} {
		if _, err := f.db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	sum := sha256.Sum256([]byte(token))
	now := time.Now().Unix()
	if _, err := f.db.Exec(`INSERT INTO identity_sessions VALUES(?,?,?,?,?)`, sum[:], user, household, now+3600, now); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: f.cookie.Name, Value: token}
}

func TestDocumentUploadDetailEditAndDownload(t *testing.T) {
	f := newFixture(t)
	data := []byte("Comprobante de compra de prueba.\n")
	d := decodeDocument(t, uploadRequest(t, f, "comprobante.txt", data, validMetadata, f.cookie))
	sum := sha256.Sum256(data)
	if d.Size != int64(len(data)) || d.SHA256 != hex.EncodeToString(sum[:]) || d.MediaType != "text/plain" || !d.HasFile || len(d.Tags) != 2 {
		t.Fatalf("incorrect metadata: %+v", d)
	}
	if d2 := decodeDocument(t, perform(f.app, "GET", "/api/documents/"+d.ID, nil, f.cookie)); d2.ID != d.ID {
		t.Fatal("wrong detail")
	}
	download := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie)
	if download.Code != 200 || !bytes.Equal(download.Body.Bytes(), data) || !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") || download.Header().Get("Cache-Control") != "no-store" || download.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe download: %d %+v", download.Code, download.Header())
	}
	req := httptest.NewRequest("GET", origin+"/api/documents/"+d.ID+"/download", nil)
	req.AddCookie(f.cookie)
	req.Header.Set("Range", "bytes=0-4")
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, req)
	if w.Code != 206 || w.Body.String() != string(data[:5]) {
		t.Fatalf("range: %d %q", w.Code, w.Body.String())
	}
	meta := map[string]any{"title": "Factura actualizada", "category": "garantias", "description": "Cobertura refrigerador", "tags": []string{"revisión"}}
	updated := decodeDocument(t, perform(f.app, "PATCH", "/api/documents/"+d.ID, meta, f.cookie))
	if updated.Title != "Factura actualizada" || updated.SHA256 != d.SHA256 || updated.Filename != d.Filename {
		t.Fatal("metadata edit modified original")
	}
	for _, path := range []string{"/api/documents?q=" + url.QueryEscape("Cobertura"), "/api/documents?q=" + url.QueryEscape("revisión"), "/api/search?q=" + url.QueryEscape("revisión")} {
		w := perform(f.app, "GET", path, nil, f.cookie)
		if w.Code != 200 || !strings.Contains(w.Body.String(), d.ID) {
			t.Fatalf("FTS metadata missing: %s", w.Body.String())
		}
	}
	for _, path := range []string{"/api/documents?category=manuales", "/api/documents?offset=50", "/api/documents?q=nonexistent"} {
		w := perform(f.app, "GET", path, nil, f.cookie)
		if w.Code != 200 || strings.Contains(w.Body.String(), d.ID) {
			t.Fatalf("filter: %s", w.Body.String())
		}
	}
	stored := filepath.Join(f.dataDir, "documents", d.ID)
	info, err := os.Stat(stored)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("private file permissions")
	}
	// Restart preserves the file, metadata, and session without regranting revoked capabilities.
	if _, err := f.db.Exec(`DELETE FROM identity_permissions WHERE capability='documents.write'`); err != nil {
		t.Fatal(err)
	}
	restarted, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got := perform(restarted, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), data) {
		t.Fatal("lost file on restart")
	}
	f.app = restarted
	if got := uploadRequest(t, f, "new.txt", data, validMetadata, f.cookie); got.Code != 403 {
		t.Fatalf("restart regranted write: %d", got.Code)
	}
}

func TestDocumentAuthorizationAllRoutes(t *testing.T) {
	f := newFixture(t)
	d := decodeDocument(t, uploadRequest(t, f, "private.txt", []byte("Private file"), validMetadata, f.cookie))
	members := []*http.Cookie{otherMember(t, f, "bob", f.state.Session.Household.ID), otherMember(t, f, "charlie", "other-home")}
	paths := []struct {
		method, path string
		body         any
	}{{"GET", "/api/documents/" + d.ID, nil}, {"GET", "/api/documents/" + d.ID + "/download", nil}, {"PATCH", "/api/documents/" + d.ID, map[string]any{"title": "stolen", "category": "otros"}}}
	for _, p := range paths {
		if got := perform(f.app, p.method, p.path, p.body, nil); got.Code != 401 {
			t.Fatalf("anonymous %s: %d", p.path, got.Code)
		}
		for _, cookie := range members {
			if got := perform(f.app, p.method, p.path, p.body, cookie); got.Code != 404 {
				t.Fatalf("foreign member %s: %d %s", p.path, got.Code, got.Body.String())
			}
		}
	}

	if err := f.app.Modules.SetEnabled(context.Background(), "documents", false); err != nil {
		t.Fatal(err)
	}
	if got := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie); got.Code != 404 {
		t.Fatal("disabled download allowed")
	}
	if err := f.app.Modules.SetEnabled(context.Background(), "documents", true); err != nil {
		t.Fatal(err)
	}
	if got := perform(f.app, "GET", "/api/documents/mine/download", nil, f.cookie); got.Code != 404 {
		t.Fatal("legacy record invented an original")
	}
	if got := uploadRequest(t, f, "new.txt", []byte("hello"), validMetadata, nil); got.Code != 401 {
		t.Fatal("anonymous upload")
	}
	if _, err := f.db.Exec(`DELETE FROM identity_permissions WHERE user_id=?`, f.state.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if got := perform(f.app, p.method, p.path, p.body, f.cookie); got.Code != 403 {
			t.Fatalf("revoked %s: %d", p.path, got.Code)
		}
	}
	if got := uploadRequest(t, f, "new.txt", []byte("hello"), validMetadata, f.cookie); got.Code != 403 {
		t.Fatal("revoked upload")
	}
}

func TestUploadValidationAndCleanup(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		name   string
		data   []byte
		meta   string
		status int
	}{
		{"empty.txt", nil, validMetadata, 400}, {"../escape.txt", []byte("hello"), validMetadata, 400}, {`C:\secret.txt`, []byte("hello"), validMetadata, 400},
		{"file.html", []byte("<script>alert(1)</script>"), validMetadata, 415}, {"fake.pdf", []byte("not a pdf"), validMetadata, 415}, {"fake.png", []byte("\x89PNG\r\n\x1a\n"), validMetadata, 415},
		{"fake.txt", []byte("<html><body>danger</body></html>"), validMetadata, 415}, {"binary.txt", []byte{0, 255, 1}, validMetadata, 415},
		{"file.txt", []byte("ok"), `{"title":"x","category":"otros","owner_id":"bob"}`, 400},
		{"file.txt", []byte("ok"), `{"title":" ","category":"otros"}`, 400}, {"file.txt", []byte("ok"), `{"title":"x","category":"unknown"}`, 400},
		{"file.txt", []byte("ok"), validMetadata + ` {}`, 400}, {"large.txt", bytes.Repeat([]byte("x"), int(documents.MaxFileSize)+1), validMetadata, 413},
	} {
		t.Run(fmt.Sprintf("%s-%d", test.name, test.status), func(t *testing.T) {
			w := uploadRequest(t, f, test.name, test.data, test.meta, f.cookie)
			if w.Code != test.status {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			entries, err := os.ReadDir(filepath.Join(f.dataDir, "documents"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed upload leaked a file: %v %v", entries, err)
			}
		})
	}
	if _, err := f.db.Exec(`CREATE TRIGGER fail_upload BEFORE INSERT ON documents BEGIN SELECT RAISE(ABORT,'test storage rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if got := uploadRequest(t, f, "fail.txt", []byte("private"), validMetadata, f.cookie); got.Code != 500 {
		t.Fatalf("DB failure: %d", got.Code)
	}
	entries, _ := os.ReadDir(filepath.Join(f.dataDir, "documents"))
	if len(entries) != 0 {
		t.Fatal("DB failure leaked file")
	}
}

func TestSupportedFormatsAndStorageConfinement(t *testing.T) {
	f := newFixture(t)
	pic := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pic.Set(0, 0, color.White)
	var pngData, jpegData bytes.Buffer
	png.Encode(&pngData, pic)
	jpeg.Encode(&jpegData, pic, nil)
	for name, data := range map[string][]byte{"scan.PNG": pngData.Bytes(), "scan.jpg": jpegData.Bytes(), "scan.pdf": []byte("%PDF-1.7\nTest PDF signature\n%%EOF\n")} {
		d := decodeDocument(t, uploadRequest(t, f, name, data, validMetadata, f.cookie))
		w := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie)
		if !bytes.Equal(data, w.Body.Bytes()) {
			t.Fatal("modified original")
		}
	}
	d := decodeDocument(t, uploadRequest(t, f, "doc.txt", []byte("private"), validMetadata, f.cookie))
	path := filepath.Join(f.dataDir, "documents", d.ID)
	os.WriteFile(path, []byte("changed size"), 0600)
	if w := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie); w.Code != 409 {
		t.Fatalf("truncated file: %d", w.Code)
	}
	os.Remove(path)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("private"), 0600)
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "GET", "/api/documents/"+d.ID+"/download", nil, f.cookie); w.Code != 404 {
		t.Fatal("symlink escaped private root")
	}
	req := httptest.NewRequest("POST", origin+"/api/documents", strings.NewReader("anything"))
	req.AddCookie(f.cookie)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("CSRF upload permitted")
	}
	for _, path := range []string{"/api/documents?offset=-1", "/api/documents?category=invalid"} {
		if w := perform(f.app, "GET", path, nil, f.cookie); w.Code != 400 {
			t.Fatal("invalid filter accepted")
		}
	}
}

func TestOwnerPermissionUpgradeIsScoped(t *testing.T) {
	f := newFixture(t)
	otherMember(t, f, "existing-reader", f.state.Session.Household.ID)
	if _, err := f.db.Exec(`DELETE FROM identity_permissions WHERE capability='documents.write'`); err != nil {
		t.Fatal(err)
	}
	// Reproduce the pre-upload installation: identity tables exist, app upgrade has not run.
	if _, err := f.db.Exec(`DELETE FROM schema_migrations WHERE module='app.document-owner-permissions'`); err != nil {
		t.Fatal(err)
	}
	restarted, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM identity_permissions WHERE capability='documents.write' AND user_id=?`, f.state.Session.User.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("owner upgrade: %d %v", count, err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM identity_permissions WHERE capability='documents.write' AND user_id='existing-reader'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("readers got automatic write access")
	}
}
