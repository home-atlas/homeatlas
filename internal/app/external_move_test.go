//go:build !race

package app_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
	"github.com/home-atlas/homeatlas/internal/testsupport/s3test"
)

func TestPersistentPluginMoveOriginalsPreservesMetadataAndSharing(t *testing.T) {
	archive, digest := documentsPackage(t)
	f := newCoreFixture(t, true)
	installDocuments(t, &f, archive, digest)
	defer func() { f.app.Close() }()
	server := s3test.New(t)
	f.app.Close()
	var err error
	f.app, err = app.NewWithOptions(context.Background(), f.db, f.assets, origin, f.dataDir, nil, app.Options{StorageHTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeDocument(t, uploadRequest(t, f, "move.txt", []byte("Documento trasladable"), validMetadata, f.cookie))
	recipient, reader := acceptMember(t, f, inviteMember(t, f, "move-reader@example.test", "member", []string{"documents.read"}, f.cookie))
	if w := perform(f.app, "PUT", "/api/documents/"+doc.ID+"/shares/"+recipient.Session.User.ID, nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	config := objectstore.Settings{Provider: "s3", S3: objectstore.S3Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "private-documents", PathStyle: true, AccessKey: "fixture-access", SecretKey: "fixture-secret"}}
	w := perform(f.app, "PATCH", "/api/household/storage", config, f.cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &config)
	stats := perform(f.app, "GET", "/api/household/storage/move", nil, f.cookie)
	if stats.Code != 200 || !strings.Contains(stats.Body.String(), `"local":1`) {
		t.Fatal("preview", stats.Code, stats.Body.String())
	}
	body := map[string]string{"revision": config.Revision, "confirm": "move-and-remove-source"}
	if w = perform(f.app, "POST", "/api/household/storage/move", body, reader); w.Code != 403 {
		t.Fatal("member moved files", w.Code)
	}
	if w = perform(f.app, "POST", "/api/household/storage/move", map[string]string{"revision": config.Revision}, f.cookie); w.Code != 400 {
		t.Fatal("unconfirmed move", w.Code)
	}
	request := httptest.NewRequest("POST", origin+"/api/household/storage/move", strings.NewReader(`{}`))
	request.AddCookie(f.cookie)
	request.Header.Set("Origin", "https://untrusted.example")
	response := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("csrf", response.Code)
	}
	if w = perform(f.app, "POST", "/api/household/storage/move", body, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"moved":true`) {
		t.Fatal("forward move", w.Code, w.Body.String())
	}
	if _, err = os.Stat(filepath.Join(f.dataDir, "documents", doc.ID)); !os.IsNotExist(err) {
		t.Fatal("local source retained", err)
	}
	if w = perform(f.app, "GET", "/api/documents/"+doc.ID+"/download", nil, reader); w.Code != 200 || w.Body.String() != "Documento trasladable" {
		t.Fatal("sharing lost", w.Code, w.Body.String())
	}
	if w = perform(f.app, "GET", "/api/documents/"+doc.ID, nil, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), doc.Title) {
		t.Fatal("metadata changed", w.Code, w.Body.String())
	}
	if w = backupRequest(f, f.cookie); w.Code != 200 {
		t.Fatal("backup after move", w.Code, w.Body.String())
	}
	config.Provider = "local"
	w = perform(f.app, "PATCH", "/api/household/storage", config, f.cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &config)
	body["revision"] = config.Revision
	if w = perform(f.app, "POST", "/api/household/storage/move", body, f.cookie); w.Code != 200 || server.Count() != 0 {
		t.Fatal("reverse move", w.Code, w.Body.String(), server.Count())
	}
	if w = perform(f.app, "GET", "/api/documents/"+doc.ID+"/download", nil, reader); w.Code != 200 || w.Body.String() != "Documento trasladable" {
		t.Fatal("reverse sharing", w.Code, w.Body.String())
	}
	if w = perform(f.app, "DELETE", "/api/documents/"+doc.ID+"/shares/"+recipient.Session.User.ID, nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w = perform(f.app, "GET", "/api/documents/"+doc.ID+"/download", nil, reader); w.Code != 404 {
		t.Fatal("revocation lost", w.Code)
	}
}
