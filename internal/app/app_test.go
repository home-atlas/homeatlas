package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/app/testdata/legacydocuments"
	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
)

const origin = "http://127.0.0.1:8080"

func perform(a *app.App, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(method, origin+path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("X-User-ID", "alice")
	req.Header.Set("X-Household-ID", "other-home")
	req.Header.Set("Authorization", "Bearer pretend-admin")
	// Even a previously attached context must not replace the cookie authentication boundary.
	req = req.WithContext(access.WithPrincipal(req.Context(), access.Principal{UserID: "alice", HouseholdID: "other-home", Permissions: map[string]bool{"documents.read": true}}))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, req)
	return w
}

type fixture struct {
	dataDir string
	app     *app.App
	db      *sql.DB
	assets  fs.FS
	state   identity.State
	cookie  *http.Cookie
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dataDir := t.TempDir()
	db, err := database.Open(filepath.Join(dataDir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	assets := fstest.MapFS{"index.html": {Data: []byte("<html>HomeAtlas</html>")}, "assets/app.js": {Data: []byte("console.log('ok')")}}
	a, err := app.New(context.Background(), db, assets, origin, dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	code, err := os.ReadFile(filepath.Join(dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	setup := perform(a, "POST", "/api/setup", map[string]string{"code": strings.TrimSpace(string(code)), "name": "Alice", "email": "alice@example.test", "password": "a long private test passphrase", "householdName": "Home A", "timezone": "America/Mexico_City"}, nil)
	if setup.Code != 201 {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body.String())
	}
	var state identity.State
	if err := json.Unmarshal(setup.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, household, owner, title string }{
		{"mine", state.Session.Household.ID, state.Session.User.ID, "Manual refrigerador"},
		{"private", state.Session.Household.ID, "bob", "Manual privado"},
		{"other-home", "other-home", state.Session.User.ID, "Manual de otro hogar"},
	} {
		if _, err := db.Exec("INSERT INTO documents (id,household_id,owner_id,title) VALUES (?,?,?,?)", row.id, row.household, row.owner, row.title); err != nil {
			t.Fatal(err)
		}
	}
	return fixture{dataDir: dataDir, app: a, db: db, assets: assets, state: state, cookie: setup.Result().Cookies()[0]}
}

func TestDocumentsAuthorizationAndHouseholdIsolation(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/api/documents", "/api/search?q=Manual"} {
		if got := perform(f.app, "GET", path, nil, nil); got.Code != 401 {
			t.Fatalf("%s: expected 401, got %d", path, got.Code)
		}
		got := perform(f.app, "GET", path, nil, f.cookie)
		if got.Code != 200 {
			t.Fatalf("%s: %d %s", path, got.Code, got.Body.String())
		}
		var records []map[string]any
		if err := json.Unmarshal(got.Body.Bytes(), &records); err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 || records[0]["id"] != "mine" {
			t.Fatalf("leaked data: %s", got.Body.String())
		}
		if got.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private API data must not be cached")
		}
	}
	for _, query := range []string{"%22", "OR%20NOT%20%2A", "%27%20OR%201%3D1--"} {
		got := perform(f.app, "GET", "/api/search?q="+query, nil, f.cookie)
		if got.Code != 200 || strings.Contains(got.Body.String(), "private") || strings.Contains(got.Body.String(), "other-home") {
			t.Fatalf("query was not handled safely: %s", got.Body.String())
		}
	}
	if _, err := f.db.Exec("DELETE FROM identity_permissions WHERE user_id=?", f.state.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	if got := perform(f.app, "GET", "/api/documents", nil, f.cookie); got.Code != 403 {
		t.Fatalf("revoked permission returned %d", got.Code)
	}
	if got := perform(f.app, "GET", "/api/search?q=Manual", nil, f.cookie); got.Code != 200 || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatalf("search leaked a module without capability: %s", got.Body.String())
	}
	if _, err := f.db.Exec("DELETE FROM identity_memberships WHERE user_id=?", f.state.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	if got := perform(f.app, "GET", "/api/documents", nil, f.cookie); got.Code != 401 {
		t.Fatalf("revoked membership returned %d", got.Code)
	}
}

func TestModuleDisablePersistsAndPreservesData(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.app.Modules.SetEnabled(ctx, "documents", false); err != nil {
		t.Fatal(err)
	}
	if got := perform(f.app, "GET", "/api/documents", nil, f.cookie); got.Code != 404 {
		t.Fatalf("disabled route returned %d", got.Code)
	}
	if got := perform(f.app, "GET", "/api/search?q=Manual", nil, f.cookie); strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatalf("disabled module appeared in search: %s", got.Body.String())
	}
	restarted, err := app.New(ctx, f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	if restarted.Modules.List()[0].Enabled {
		t.Fatal("module state was not persisted")
	}
	if err := restarted.Modules.SetEnabled(ctx, "documents", true); err != nil {
		t.Fatal(err)
	}
	got := perform(restarted, "GET", "/api/documents", nil, f.cookie)
	if got.Code != 200 || !strings.Contains(got.Body.String(), "refrigerador") {
		t.Fatalf("document lost on disable or session lost after restart: %s", got.Body.String())
	}
	duplicate, err := documents.New(f.db, filepath.Join(f.dataDir, "documents"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer duplicate.Close()
	if err := restarted.Modules.Register(ctx, duplicate); err == nil {
		t.Fatal("duplicate registration accepted")
	}
}

func TestHTTPFoundation(t *testing.T) {
	f := newFixture(t)
	for path, status := range map[string]int{"/api/health": 200, "/api/modules": 401, "/": 200, "/documents": 200, "/setup": 200, "/login": 200, "/missing.js": 404, "/api/missing": 404} {
		got := perform(f.app, "GET", path, nil, nil)
		if got.Code != status {
			t.Errorf("%s: got %d, want %d", path, got.Code, status)
		}
		if got.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error("missing security headers")
		}
	}
	if got := perform(f.app, "GET", "/api/modules", nil, f.cookie); !strings.Contains(got.Body.String(), `"contractVersion":"1"`) {
		t.Fatal("missing versioned module manifest")
	}
}
