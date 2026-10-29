package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

func currentModules(t *testing.T, f fixture, cookie *http.Cookie) []modules.Status {
	t.Helper()
	w := perform(f.app, "GET", "/api/modules", nil, cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result []modules.Status
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHouseholdSettingsAuthorizationValidationAndRestart(t *testing.T) {
	f := newFixture(t)
	body := map[string]string{"name": "  Casa Atlás  ", "timezone": "Asia/Tokyo"}
	if w := perform(f.app, "GET", "/api/household/settings", nil, nil); w.Code != 401 {
		t.Fatal("public settings")
	}
	if w := perform(f.app, "PATCH", "/api/household/settings", body, nil); w.Code != 401 {
		t.Fatal("anonymous settings update")
	}
	member := otherMember(t, f, "reader", f.state.Session.Household.ID)
	if w := perform(f.app, "GET", "/api/household/settings", nil, member); w.Code != 200 || !strings.Contains(w.Body.String(), `"canManage":false`) {
		t.Fatal("member cannot read settings", w.Code, w.Body.String())
	}
	if w := perform(f.app, "PATCH", "/api/household/settings", body, member); w.Code != 403 {
		t.Fatal("member can edit settings")
	}
	if _, err := f.db.Exec(`INSERT INTO identity_permissions VALUES('reader',?,'household.manage')`, f.state.Session.Household.ID); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "PATCH", "/api/household/settings", body, member); w.Code != 403 {
		t.Fatal("member with forged management capability bypassed role")
	}
	for _, invalid := range []any{
		map[string]string{"name": "", "timezone": "UTC"}, map[string]string{"name": strings.Repeat("a", 101), "timezone": "UTC"}, map[string]string{"name": "bad\nname", "timezone": "UTC"},
		map[string]string{"name": "Home", "timezone": "Local"}, map[string]string{"name": "Home", "timezone": "not/a/zone"},
		map[string]string{"name": "Home", "timezone": "UTC", "householdId": "another-home"},
	} {
		if w := perform(f.app, "PATCH", "/api/household/settings", invalid, f.cookie); w.Code != 400 {
			t.Fatal("invalid settings accepted", w.Code, w.Body.String())
		}
	}
	if w := perform(f.app, "PATCH", "/api/household/settings", body, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/household/settings", "/api/auth/session"} {
		w := perform(f.app, "GET", path, nil, f.cookie)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Casa Atlás"`) || !strings.Contains(w.Body.String(), `"timezone":"Asia/Tokyo"`) {
			t.Fatal("settings not reflected in session", w.Body.String())
		}
	}
	if _, err := f.db.Exec(`UPDATE identity_memberships SET role='admin' WHERE user_id='reader'`); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "PATCH", "/api/household/settings", map[string]string{"name": "Admin update", "timezone": "UTC"}, member); w.Code != 204 {
		t.Fatal("authorized admin denied", w.Code, w.Body.String())
	}
	if _, err := f.db.Exec(`DELETE FROM identity_permissions WHERE user_id='reader' AND capability='household.manage'`); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "PATCH", "/api/household/settings", body, member); w.Code != 403 {
		t.Fatal("revoked management still works")
	}
	restarted, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if w := perform(restarted, "GET", "/api/household/settings", nil, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), "Admin update") {
		t.Fatal("settings lost on restart")
	}
}

func TestHouseholdModuleTogglePreservesFilesGrantsAndOtherHouseholds(t *testing.T) {
	f := newFixture(t)
	member := otherMember(t, f, "reader", f.state.Session.Household.ID)
	other := otherMember(t, f, "other", "other-home")
	data := []byte("original document survives module toggles")
	doc := decodeDocument(t, uploadRequest(t, f, "original.txt", data, validMetadata, f.cookie))
	base := "/api/documents/" + doc.ID
	if w := perform(f.app, "PUT", base+"/shares/reader", nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": false}, member); w.Code != 403 {
		t.Fatal("member can disable module")
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": false}, nil); w.Code != 401 {
		t.Fatal("anonymous can disable module")
	}
	if _, err := f.db.Exec(`UPDATE identity_memberships SET role='admin' WHERE user_id='reader';
 INSERT INTO identity_permissions SELECT 'reader',household_id,'household.manage' FROM identity_memberships WHERE user_id='reader';`); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": false}, member); w.Code != 204 {
		t.Fatal("authorized admin denied module change", w.Code, w.Body.String())
	}
	if _, err := f.db.Exec(`DELETE FROM identity_permissions WHERE user_id='reader' AND capability='household.manage'`); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": true}, member); w.Code != 403 {
		t.Fatal("revoked module manager remained authorized")
	}
	for _, body := range []any{map[string]string{}, map[string]string{"enabled": "false"}, map[string]any{"enabled": true, "householdId": "other-home"}} {
		if w := perform(f.app, "PATCH", "/api/household/modules/documents", body, f.cookie); w.Code != 400 {
			t.Fatal("invalid state accepted", w.Code)
		}
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/unknown", map[string]bool{"enabled": false}, f.cookie); w.Code != 404 {
		t.Fatal("unknown module accepted")
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": false}, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if currentModules(t, f, f.cookie)[0].Enabled || currentModules(t, f, member)[0].Enabled {
		t.Fatal("household state was not shared with members")
	}
	if !currentModules(t, f, other)[0].Enabled {
		t.Fatal("another household was disabled")
	}
	for _, cookie := range []*http.Cookie{f.cookie, member} {
		for _, path := range []string{"/api/documents", base, base + "/download"} {
			if w := perform(f.app, "GET", path, nil, cookie); w.Code != 404 {
				t.Fatal("disabled module remained accessible", path, w.Code)
			}
		}
		if w := perform(f.app, "GET", "/api/search?q=Factura", nil, cookie); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatal("disabled module leaked search")
		}
	}
	if w := perform(f.app, "GET", "/api/documents", nil, other); w.Code != 200 {
		t.Fatal("other household route disabled")
	}
	if w := perform(f.app, "GET", "/api/household/settings", nil, f.cookie); w.Code != 200 {
		t.Fatal("module toggle disabled Core")
	}
	restarted, err := app.New(context.Background(), f.db, f.assets, origin, f.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if w := perform(restarted, "GET", base, nil, f.cookie); w.Code != 404 {
		t.Fatal("disabled state lost on restart")
	}
	if w := perform(restarted, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": true}, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, cookie := range []*http.Cookie{f.cookie, member} {
		if w := perform(restarted, "GET", base+"/download", nil, cookie); w.Code != 200 || w.Body.String() != string(data) {
			t.Fatal("reactivation lost file or sharing grant", w.Code, w.Body.String())
		}
	}
	if w := perform(restarted, "GET", "/api/documents/private", nil, f.cookie); w.Code != 404 {
		t.Fatal("reactivation expanded private access")
	}
}

func TestSettingsAndModuleChangesAreAtomicAndProtectedFromCSRF(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(`CREATE TRIGGER reject_settings BEFORE UPDATE ON identity_households BEGIN SELECT RAISE(ABORT,'test rollback'); END;
 CREATE TRIGGER reject_module BEFORE INSERT ON household_module_states BEGIN SELECT RAISE(ABORT,'test rollback'); END;`); err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "PATCH", "/api/household/settings", map[string]string{"name": "Rejected", "timezone": "UTC"}, f.cookie); w.Code != 503 {
		t.Fatal("settings SQL failure ignored")
	}
	if w := perform(f.app, "GET", "/api/household/settings", nil, f.cookie); strings.Contains(w.Body.String(), "Rejected") {
		t.Fatal("failed settings were saved")
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": false}, f.cookie); w.Code != 503 {
		t.Fatal("module SQL failure ignored")
	}
	if !currentModules(t, f, f.cookie)[0].Enabled {
		t.Fatal("failed toggle changed state")
	}
	for _, path := range []string{"/api/household/settings", "/api/household/modules/documents"} {
		r := httptest.NewRequest("PATCH", origin+path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://evil.example.test")
		r.AddCookie(f.cookie)
		w := httptest.NewRecorder()
		f.app.Handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-origin configuration accepted")
		}
	}
}

func TestConcurrentModuleChangesAndSearchFinishWithoutDeadlock(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	failures := make(chan int, 24)
	for i := range 12 {
		wg.Go(func() {
			w := perform(f.app, "PATCH", "/api/household/modules/documents", map[string]bool{"enabled": i%2 == 0}, f.cookie)
			if w.Code != 204 {
				failures <- w.Code
			}
		})
		wg.Go(func() {
			w := perform(f.app, "GET", "/api/search?q=Manual", nil, f.cookie)
			if w.Code != 200 {
				failures <- w.Code
			}
		})
	}
	wg.Wait()
	close(failures)
	for status := range failures {
		t.Fatal("concurrent operation failed", status)
	}
}
