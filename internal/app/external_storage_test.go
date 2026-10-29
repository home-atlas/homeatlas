//go:build !race

package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
	"github.com/home-atlas/homeatlas/internal/testsupport/s3test"
)

func TestPersistentPluginS3StorageSwitchBackupAndRecovery(t *testing.T) {
	archive, digest := documentsPackage(t)
	f := newCoreFixture(t, true)
	installDocuments(t, &f, archive, digest)
	defer func() { f.app.Close() }()
	local := decodeDocument(t, uploadRequest(t, f, "local.txt", []byte("Original local"), validMetadata, f.cookie))
	server := s3test.New(t)
	restart := func() {
		f.app.Close()
		var err error
		f.app, err = app.NewWithOptions(context.Background(), f.db, f.assets, origin, f.dataDir, nil, app.Options{StorageHTTPClient: server.Client()})
		if err != nil {
			t.Fatal(err)
		}
	}
	restart()
	config := objectstore.Settings{Provider: "s3", S3: objectstore.S3Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "private-documents", Prefix: "originals", PathStyle: true, AccessKey: "fixture-access", SecretKey: "fixture-secret"}}
	w := perform(f.app, "PATCH", "/api/household/storage", config, f.cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "fixture-") {
		t.Fatal("credential response")
	}
	json.Unmarshal(w.Body.Bytes(), &config)
	remote := decodeDocument(t, uploadRequest(t, f, "remote.txt", []byte("Original remoto"), validMetadata, f.cookie))
	if server.Count() != 1 {
		t.Fatal("remote original missing", server.Count())
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "documents", remote.ID)); !os.IsNotExist(err) {
		t.Fatal("S3 upload retained local original", err)
	}
	_, reader := acceptMember(t, f, inviteMember(t, f, "s3-reader@example.test", "member", []string{"documents.read"}, f.cookie))
	if w = perform(f.app, "GET", "/api/documents/"+remote.ID+"/download", nil, reader); w.Code != 404 {
		t.Fatal("remote private original leaked", w.Code)
	}
	copy := backupRequest(f, f.cookie)
	if copy.Code != 200 {
		t.Fatal("portable backup", copy.Code, copy.Body.String())
	}
	for name, content := range archiveEntries(t, copy.Body.Bytes()) {
		if bytes.Contains(content, []byte("fixture-secret")) || bytes.Contains(content, []byte("file_storage_profiles")) {
			t.Fatal("connection credentials/schema exported", name)
		}
	}
	config.Provider = "local"
	if w = perform(f.app, "PATCH", "/api/household/storage", config, f.cookie); w.Code != 200 {
		t.Fatal("switch local", w.Code, w.Body.String())
	}
	restart()
	for _, item := range []struct{ id, text string }{{local.ID, "Original local"}, {remote.ID, "Original remoto"}} {
		if w = perform(f.app, "GET", "/api/documents/"+item.id+"/download", nil, f.cookie); w.Code != 200 || w.Body.String() != item.text {
			t.Fatal("switch/restart", w.Code, w.Body.String())
		}
	}
	// Ranges are served through the authenticated host, never through a public/presigned object URL.
	req, _ := http.NewRequest("GET", origin+"/api/documents/"+remote.ID+"/download", nil)
	req.AddCookie(f.cookie)
	req.Header.Set("Range", "bytes=0-7")
	rw := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(rw, req)
	if rw.Code != 206 || rw.Body.String() != "Original" {
		t.Fatal("remote range", rw.Code, rw.Body.String())
	}
	server.Deny(true)
	if w = perform(f.app, "GET", "/api/documents/"+remote.ID+"/download", nil, f.cookie); w.Code != 503 {
		t.Fatal("remote failure", w.Code)
	}
	if w = restoreRequest(f, copy.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal("restore independent from S3", w.Code, w.Body.String())
	}
	if w = perform(f.app, "GET", "/api/documents/"+remote.ID+"/download", nil, f.cookie); w.Code != 200 || w.Body.String() != "Original remoto" {
		t.Fatal("restored local original", w.Code, w.Body.String())
	}
	empty := newCoreFixture(t, false)
	installDocuments(t, &empty, archive, digest)
	defer func() { empty.app.Close() }()
	code, err := os.ReadFile(filepath.Join(empty.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	recovered := encryptedRequest(empty, "POST", "/api/setup/restore", copy.Body.Bytes(), base64.StdEncoding.EncodeToString([]byte(testBackupPassword)), strings.TrimSpace(string(code)))
	if recovered.Code != 201 {
		t.Fatal("new recovery", recovered.Code, recovered.Body.String())
	}
	login := perform(empty.app, "POST", "/api/auth/login", map[string]string{"identifier": "core-owner", "password": invitedPassword}, nil)
	if login.Code != 200 {
		t.Fatal("recovered owner login", login.Code, login.Body.String())
	}
	for _, cookie := range login.Result().Cookies() {
		if strings.Contains(cookie.Name, "session") {
			empty.cookie = cookie
		}
	}
	if empty.cookie == nil {
		t.Fatal("missing recovered owner session")
	}
	if w = perform(empty.app, "GET", "/api/documents/"+remote.ID+"/download", nil, empty.cookie); w.Code != 200 || w.Body.String() != "Original remoto" {
		t.Fatal("new recovery local", w.Code, w.Body.String())
	}
	if w = perform(empty.app, "GET", "/api/household/storage", nil, empty.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"provider":"local"`) {
		t.Fatal("new recovery imported storage", w.Code, w.Body.String())
	}
	staged, _ := os.ReadDir(filepath.Join(f.dataDir, "file-staging"))
	if len(staged) != 0 {
		t.Fatal("temporary remote files retained", len(staged))
	}
}
