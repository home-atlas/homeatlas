package app_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

func TestStorageSettingsDefaultPermissionsCSRFAndValidation(t *testing.T) {
	f := newCoreFixture(t, true)
	if w := perform(f.app, "GET", "/api/household/storage", nil, nil); w.Code != 401 {
		t.Fatal("anonymous", w.Code)
	}
	w := perform(f.app, "GET", "/api/household/storage", nil, f.cookie)
	var current objectstore.Settings
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &current) != nil || current.Provider != "local" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = perform(f.app, "PATCH", "/api/household/storage", map[string]any{"provider": "local", "revision": ""}, f.cookie); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = perform(f.app, "PATCH", "/api/household/storage", map[string]any{"provider": "s3"}, f.cookie); w.Code != 409 {
		t.Fatal("stale", w.Code, w.Body.String())
	}
	w = perform(f.app, "GET", "/api/household/storage", nil, f.cookie)
	json.Unmarshal(w.Body.Bytes(), &current)
	current.Provider = "s3"
	current.S3 = objectstore.S3Config{Endpoint: "http://insecure.example", Region: "us-east-1", Bucket: "private-test", AccessKey: "key", SecretKey: "secret"}
	if w = perform(f.app, "PATCH", "/api/household/storage", current, f.cookie); w.Code != 400 {
		t.Fatal("invalid", w.Code, w.Body.String())
	}
	request := httptest.NewRequest("PATCH", origin+"/api/household/storage", strings.NewReader(`{"provider":"local"}`))
	request.AddCookie(f.cookie)
	request.Header.Set("Origin", "https://untrusted.example")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("csrf", response.Code)
	}
	cookie := otherMember(t, f, "storage-admin", f.state.Session.Household.ID)
	if _, err := f.db.Exec("UPDATE identity_memberships SET role='admin' WHERE user_id='storage-admin'; INSERT INTO identity_permissions VALUES('storage-admin',?,'household.manage')", f.state.Session.Household.ID); err != nil {
		t.Fatal(err)
	}
	if w = perform(f.app, "GET", "/api/household/storage", nil, cookie); w.Code != 403 {
		t.Fatal("admin sees credentials configuration", w.Code)
	}
	if w = perform(f.app, "PATCH", "/api/household/storage", current, cookie); w.Code != 403 {
		t.Fatal("admin changes storage", w.Code)
	}
}
