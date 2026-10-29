package identity

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func assistedFixture(t *testing.T) (*Service, http.Handler, *http.Cookie) {
	t.Helper()
	s, h, _ := newService(t, testOrigin)
	s.ownerCapabilities = []string{Manage, "documents.read", "documents.write", "documents.share"}
	s.capabilities = []string{"documents.read", "documents.write", "documents.share"}
	return s, h, bootstrap(t, h, testOrigin)
}
func newAssisted(t *testing.T, h http.Handler, owner *http.Cookie, username string) (string, string) {
	t.Helper()
	w := send(h, testOrigin, "POST", "/api/household/assisted-members", map[string]any{"name": "María", "username": username, "role": "restricted", "permissions": []string{"documents.read"}, "password": testPassword}, owner)
	if w.Code != 201 {
		t.Fatalf("assisted creation: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		URL  string `json:"url"`
		User string `json:"userId"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	return strings.Split(result.URL, "#")[1], result.User
}
func activatePIN(t *testing.T, h http.Handler, token, pin string) *http.Cookie {
	t.Helper()
	w := send(h, testOrigin, "POST", "/api/assisted/complete", map[string]string{"token": token, "pin": pin, "deviceName": "Tablet personal"}, nil)
	if w.Code != 204 {
		t.Fatalf("activation: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("activation unexpectedly started a session")
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe device cookie")
	}
	return cookies[0]
}
func TestOptionalEmailAndUsernameLogin(t *testing.T) {
	_, h, db := newService(t, testOrigin)
	body := setupBody()
	body["email"] = ""
	body["username"] = "mi-casa"
	w := send(h, testOrigin, "POST", "/api/setup", body, nil)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var state State
	json.Unmarshal(w.Body.Bytes(), &state)
	if state.Session.User.Email != "" || state.Session.User.Username != "mi-casa" {
		t.Fatal(state)
	}
	var absent bool
	if err := db.QueryRow("SELECT email IS NULL FROM identity_users").Scan(&absent); err != nil || !absent {
		t.Fatal("email was fabricated", err)
	}
	w = send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"identifier": "MI-CASA", "password": testPassword}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, _, err := IssueRecovery(context.Background(), db, "mi-casa"); err != nil {
		t.Fatal("username recovery unavailable", err)
	}
}
func TestAssistedEnrollmentAndDeviceBoundPIN(t *testing.T) {
	s, h, owner := assistedFixture(t)
	token, user := newAssisted(t, h, owner, "maria")
	if w := send(h, testOrigin, "POST", "/api/assisted/inspect", map[string]string{"token": token}, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	device := activatePIN(t, h, token, "642817")
	if w := send(h, testOrigin, "POST", "/api/assisted/complete", map[string]string{"token": token, "pin": "642817", "deviceName": "Otro equipo"}, nil); w.Code != 410 {
		t.Fatal("enrollment replay", w.Code)
	}
	var pinHash string
	var stored []byte
	if err := s.db.QueryRow("SELECT pin_hash,token_hash FROM identity_pin_devices WHERE user_id=?", user).Scan(&pinHash, &stored); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(device.Value))
	if string(stored) != string(digest[:]) || pinHash == "642817" || !verifyPassword("642817", pinHash) {
		t.Fatal("credential storage unsafe")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "642817"}, nil); w.Code != 401 {
		t.Fatal("PIN worked without bound cookie", w.Code)
	}
	for range 4 {
		if w := send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "111111"}, device); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	w := send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "642817"}, device)
	if w.Code != 200 {
		t.Fatal("fifth attempt should accept valid PIN", w.Code, w.Body.String())
	}
	var state State
	json.Unmarshal(w.Body.Bytes(), &state)
	if state.Session.User.ID != user || state.Session.Role != "restricted" || state.Session.User.Email != "" {
		t.Fatal(state)
	}
	for _, cap := range state.Session.Permissions {
		if strings.HasPrefix(cap, "household.") {
			t.Fatal("assisted access elevated permissions")
		}
	}
	cookie := w.Result().Cookies()[0]
	if w := send(h, testOrigin, "GET", "/api/probe", nil, cookie); w.Code != 200 {
		t.Fatal("PIN session not authorized", w.Code)
	}
	if w := send(h, testOrigin, "GET", "/api/household/members", nil, cookie); w.Code != 200 || strings.Contains(w.Body.String(), "alice@example.test") {
		t.Fatal("private member email disclosed")
	}
	code, _, err := IssueRecovery(context.Background(), s.db, "maria")
	if err != nil {
		t.Fatal(err)
	}
	w = send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": "a recovered private passphrase"}, nil)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "642817"}, device); w.Code != 401 {
		t.Fatal("recovery retained device access")
	}
	if w := send(h, testOrigin, "GET", "/api/probe", nil, cookie); w.Code != 401 {
		t.Fatal("recovery retained session")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"identifier": "maria", "password": "a recovered private passphrase"}, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestPINLockPersistsAndEnrollmentRevokesOldDevice(t *testing.T) {
	s, h, owner := assistedFixture(t)
	token, user := newAssisted(t, h, owner, "abuela")
	device := activatePIN(t, h, token, "642817")
	for range 5 {
		send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "111111"}, device)
	}
	restarted, err := New(context.Background(), s.db, Config{PublicURL: testOrigin, OwnerCapabilities: s.ownerCapabilities, DelegableCapabilities: s.capabilities})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	h = handler(restarted)
	if w := send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "642817"}, device); w.Code != 401 {
		t.Fatal("locked device survived restart unlocked")
	}
	w := send(h, testOrigin, "POST", "/api/household/members/"+user+"/enrollment", map[string]string{"password": testPassword}, owner)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var link struct{ URL string }
	json.Unmarshal(w.Body.Bytes(), &link)
	second := activatePIN(t, h, strings.Split(link.URL, "#")[1], "728416")
	if w := send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "642817"}, device); w.Code != 401 {
		t.Fatal("replaced binding retained access")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "728416"}, second); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestAssistedEnrollmentRequiresFreshManagerAndCurrentGrant(t *testing.T) {
	s, h, owner := assistedFixture(t)
	body := map[string]any{"name": "María", "username": "maria", "role": "restricted", "permissions": []string{"documents.read"}, "password": "wrong"}
	if w := send(h, testOrigin, "POST", "/api/household/assisted-members", body, owner); w.Code != 403 {
		t.Fatal("missing reauthentication accepted", w.Code)
	}
	body["password"] = testPassword
	body["role"] = "admin"
	if w := send(h, testOrigin, "POST", "/api/household/assisted-members", body, owner); w.Code != 400 {
		t.Fatal("assisted admin allowed", w.Code)
	}
	token, _ := newAssisted(t, h, owner, "maria")
	if _, err := s.db.Exec("DELETE FROM identity_permissions WHERE capability=?", Manage); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/assisted/complete", map[string]string{"token": token, "pin": "642817", "deviceName": "Tablet"}, nil); w.Code != 410 {
		t.Fatal("demoted issuer link accepted", w.Code)
	}
}

func TestPINRegistrationAndRevocationAreScoped(t *testing.T) {
	s, h, owner := assistedFixture(t)
	token, user := newAssisted(t, h, owner, "maria")
	activatePIN(t, h, token, "642817")
	code, _, err := IssueRecovery(context.Background(), s.db, "maria")
	if err != nil {
		t.Fatal(err)
	}
	w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": testPassword}, nil)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"identifier": "maria", "password": testPassword}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	for _, attempt := range []struct {
		cookie   *http.Cookie
		password string
		status   int
	}{{owner, testPassword, 403}, {cookie, "wrong", 403}, {cookie, testPassword, 204}} {
		w = send(h, testOrigin, "POST", "/api/auth/pin/device", map[string]string{"pin": "728416", "password": attempt.password, "deviceName": "Tablet personal"}, attempt.cookie)
		if w.Code != attempt.status {
			t.Fatal("PIN registration", w.Code, w.Body.String())
		}
	}
	var id string
	if err = s.db.QueryRow("SELECT id FROM identity_pin_devices WHERE user_id=?", user).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if w = send(h, testOrigin, "DELETE", "/api/auth/pin/devices/"+id, nil, owner); w.Code != 404 {
		t.Fatal("another account revoked device", w.Code)
	}
	if w = send(h, testOrigin, "GET", "/api/auth/access", nil, owner); w.Code != 200 {
		t.Fatal("failed revoke invalidated unrelated session")
	}
	if w = send(h, testOrigin, "DELETE", "/api/auth/pin/devices/"+id, nil, cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w = send(h, testOrigin, "GET", "/api/probe", nil, cookie); w.Code != 401 {
		t.Fatal("device revocation retained sessions")
	}
}

func TestUsernameAndEmailSharePasswordAttemptLimit(t *testing.T) {
	_, h, db := newService(t, testOrigin)
	bootstrap(t, h, testOrigin)
	var username string
	if err := db.QueryRow("SELECT username FROM identity_users").Scan(&username); err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("account:alice@example.test"))
	if _, err := db.Exec("UPDATE identity_login_attempts SET attempts=9 WHERE key=?", key[:]); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"identifier": username, "password": "wrong"}, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"email": "alice@example.test", "password": testPassword}, nil); w.Code != 429 {
		t.Fatal("alias bypassed account throttle", w.Code)
	}
}

func TestPINSessionCannotBecomeAdministrativeAfterPermissionChange(t *testing.T) {
	s, h, owner := assistedFixture(t)
	token, user := newAssisted(t, h, owner, "maria")
	device := activatePIN(t, h, token, "642817")
	w := send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "642817"}, device)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if w = send(h, testOrigin, "PATCH", "/api/household/members/"+user, map[string]any{"role": "admin", "permissions": []string{"documents.read"}}, owner); w.Code != 403 {
		t.Fatal("PIN-only account was promoted", w.Code)
	}
	if _, err := s.db.Exec("INSERT INTO identity_permissions(user_id,household_id,capability) SELECT user_id,household_id,? FROM identity_memberships WHERE user_id=?", Manage, user); err != nil {
		t.Fatal(err)
	}
	if w = send(h, testOrigin, "GET", "/api/auth/session", nil, cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"session":null`) {
		t.Fatal("PIN session inherited management access", w.Code, w.Body.String())
	}
	if w = send(h, testOrigin, "POST", "/api/auth/pin/login", map[string]string{"pin": "642817"}, device); w.Code != 401 {
		t.Fatal("PIN device retained privileged access", w.Code)
	}
}

func TestConcurrentAssistedActivationConsumesOneLink(t *testing.T) {
	_, h, owner := assistedFixture(t)
	token, _ := newAssisted(t, h, owner, "maria")
	start := make(chan struct{})
	statuses := make(chan int, 2)
	for range 2 {
		go func() {
			<-start
			w := send(h, testOrigin, "POST", "/api/assisted/complete", map[string]string{"token": token, "pin": "642817", "deviceName": "Mi equipo"}, nil)
			statuses <- w.Code
		}()
	}
	close(start)
	first, second := <-statuses, <-statuses
	if !((first == 204 && second == 410) || (first == 410 && second == 204)) {
		t.Fatal("link did not have one atomic consumer", first, second)
	}
}
