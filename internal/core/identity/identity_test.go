package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

const testOrigin = "http://127.0.0.1:8080"
const testPassword = "a private passphrase for testing"
const testSetupCode = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func newService(t *testing.T, origin string) (*Service, http.Handler, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	path := filepath.Join(dir, "setup-code")
	if err := os.WriteFile(path, []byte(testSetupCode), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), db, Config{PublicURL: origin, SetupCodePath: path, OwnerCapabilities: []string{"documents.read"}})
	if err != nil {
		t.Fatal(err)
	}
	return s, handler(s), db
}

func handler(s *Service) http.Handler {
	mux := http.NewServeMux()
	s.Mount(mux)
	mux.HandleFunc("GET /api/probe", func(w http.ResponseWriter, r *http.Request) {
		if !modules.Authorize(w, r, "documents.read") {
			return
		}
		p, _ := access.Current(r.Context())
		modules.JSON(w, 200, p.UserID)
	})
	return s.Middleware(mux)
}

func send(h http.Handler, origin, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(body)
	r := httptest.NewRequest(method, origin+path, bytes.NewReader(encoded))
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "192.0.2.10:12345"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func setupBody() map[string]string {
	return map[string]string{"code": testSetupCode, "name": "Alice", "email": "Alice@Example.test", "password": testPassword, "householdName": "Nuestro hogar", "timezone": "America/Mexico_City"}
}

func bootstrap(t *testing.T, h http.Handler, origin string) *http.Cookie {
	t.Helper()
	w := send(h, origin, "POST", "/api/setup", setupBody(), nil)
	if w.Code != 201 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected a single session cookie, got %d", len(cookies))
	}
	return cookies[0]
}

func TestSetupLoginRotationLogoutAndRestart(t *testing.T) {
	s, h, db := newService(t, testOrigin)
	initial := send(h, testOrigin, "GET", "/api/auth/session", nil, nil)
	if !strings.Contains(initial.Body.String(), `"setupRequired":true`) {
		t.Fatal(initial.Body.String())
	}
	cookie := bootstrap(t, h, testOrigin)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != int(sessionLifetime.Seconds()) {
		t.Fatalf("unsafe cookie attributes: %v", cookie)
	}
	if cookie.Secure {
		t.Fatal("loopback development cookie should work over HTTP")
	}
	var hash string
	if err := db.QueryRow("SELECT password_hash FROM identity_users").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") || !verifyPassword(testPassword, hash) || verifyPassword("incorrect password", hash) {
		t.Fatal("password storage is not correct")
	}
	var stored []byte
	if err := db.QueryRow("SELECT token_hash FROM identity_sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	if !bytes.Equal(stored, digest[:]) || bytes.Contains(stored, []byte(cookie.Value)) {
		t.Fatal("raw session token was stored")
	}
	duplicate := send(h, testOrigin, "POST", "/api/setup", setupBody(), nil)
	if duplicate.Code != 409 {
		t.Fatalf("setup was repeatable: %d", duplicate.Code)
	}
	for _, email := range []string{"alice@example.test", "unknown@example.test"} {
		w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"email": email, "password": "incorrect password"}, nil)
		if w.Code != 401 || strings.TrimSpace(w.Body.String()) != `{"error":"invalid_credentials"}` || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("credentials response leaked information: %d %s", w.Code, w.Body.String())
		}
	}
	login := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"email": " ALICE@example.test ", "password": testPassword}, cookie)
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	newCookie := login.Result().Cookies()[0]
	if newCookie.Value == cookie.Value {
		t.Fatal("session token was not rotated")
	}
	if got := send(h, testOrigin, "GET", "/api/probe", nil, cookie); got.Code != 401 {
		t.Fatal("previous login session can be replayed")
	}
	for _, value := range []string{"forged", strings.Repeat("a", 43), newCookie.Value[:42] + "!"} {
		forged := *newCookie
		forged.Value = value
		if got := send(h, testOrigin, "GET", "/api/probe", nil, &forged); got.Code != 401 {
			t.Fatal("forged session accepted")
		}
	}
	restarted, err := New(context.Background(), db, Config{PublicURL: testOrigin, OwnerCapabilities: s.ownerCapabilities})
	if err != nil {
		t.Fatal(err)
	}
	h = handler(restarted)
	if got := send(h, testOrigin, "GET", "/api/probe", nil, newCookie); got.Code != 200 {
		t.Fatal("session did not survive service restart")
	}
	logout := send(h, testOrigin, "POST", "/api/auth/logout", nil, newCookie)
	if logout.Code != 204 || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear the cookie")
	}
	if got := send(h, testOrigin, "GET", "/api/probe", nil, newCookie); got.Code != 401 {
		t.Fatal("logged-out session can be replayed")
	}
	if got := send(h, testOrigin, "POST", "/api/auth/logout", nil, newCookie); got.Code != 204 {
		t.Fatal("logout is not idempotent")
	}
}

func TestSetupIsAtomicAndSingleUse(t *testing.T) {
	_, h, db := newService(t, testOrigin)
	if _, err := db.Exec(`CREATE TRIGGER fail_membership BEFORE INSERT ON identity_memberships BEGIN SELECT RAISE(ABORT, 'test rollback'); END;`); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/setup", setupBody(), nil); w.Code != 500 {
		t.Fatal(w.Code)
	}
	for _, table := range []string{"identity_users", "identity_households", "identity_memberships", "identity_installation", "identity_sessions"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial setup in %s: %d, %v", table, count, err)
		}
	}
	if _, err := db.Exec("DROP TRIGGER fail_membership"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() { statuses <- send(h, testOrigin, "POST", "/api/setup", setupBody(), nil).Code })
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[201] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent setup: %v", counts)
	}
	for _, table := range []string{"identity_users", "identity_households", "identity_memberships", "identity_installation", "identity_sessions"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("duplicate setup in %s: %d, %v", table, count, err)
		}
	}
}

func TestSessionExpiryAndMembershipRevocation(t *testing.T) {
	for _, update := range []string{
		"UPDATE identity_sessions SET expires_at=0",
		"UPDATE identity_sessions SET last_seen=0",
		"DELETE FROM identity_memberships",
	} {
		t.Run(update, func(t *testing.T) {
			_, h, db := newService(t, testOrigin)
			cookie := bootstrap(t, h, testOrigin)
			if _, err := db.Exec(update); err != nil {
				t.Fatal(err)
			}
			w := send(h, testOrigin, "GET", "/api/probe", nil, cookie)
			if w.Code != 401 {
				t.Fatalf("expired/revoked session accepted: %d", w.Code)
			}
			if w.Result().Cookies()[0].MaxAge != -1 {
				t.Fatal("stale cookie not cleared")
			}
		})
	}
}

func TestHTTPSAndCrossOriginProtection(t *testing.T) {
	const origin = "https://home.example.test"
	_, h, _ := newService(t, origin)
	cookie := bootstrap(t, h, origin)
	if !cookie.Secure || !strings.HasPrefix(cookie.Name, "__Host-") {
		t.Fatal("HTTPS cookie lacks Secure/Host prefix")
	}
	for _, path := range []string{"/api/setup", "/api/auth/login", "/api/auth/logout", "/api/auth/recover", "/api/auth/email/verification", "/api/auth/email/verify", "/api/auth/recovery/request"} {
		r := httptest.NewRequest("POST", origin+path, strings.NewReader("{}"))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://evil.example.test")
		r.Header.Set("Sec-Fetch-Site", "same-site")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("cross-origin %s returned %d", path, w.Code)
		}
	}
	if w := send(h, origin, "GET", "/api/probe", nil, cookie); w.Code != 200 {
		t.Fatal("CSRF request logged out the user")
	}
	if w := send(h, origin, "GET", "/api/auth/logout", nil, cookie); w.Code != 405 {
		t.Fatal("GET logout changed state")
	}
	if w := send(h, "https://evil.example.test", "POST", "/api/auth/logout", nil, cookie); w.Code != 421 {
		t.Fatal("unexpected Host accepted")
	}
	if w := send(h, origin, "POST", "/api/auth/logout", nil, cookie); w.Code != 204 {
		t.Fatal("same-origin logout failed")
	}
}

func TestValidationAndRateLimiting(t *testing.T) {
	s, h, db := newService(t, testOrigin)
	invalid := setupBody()
	invalid["password"] = "short"
	if w := send(h, testOrigin, "POST", "/api/setup", invalid, nil); w.Code != 400 {
		t.Fatal("weak password accepted")
	}
	invalid = setupBody()
	invalid["timezone"] = "not/a/timezone"
	if w := send(h, testOrigin, "POST", "/api/setup", invalid, nil); w.Code != 400 {
		t.Fatal("invalid timezone accepted")
	}
	for _, test := range []struct {
		body, contentType string
		status            int
	}{
		{`{"email":"alice@example.test","password":"x","role":"owner"}`, "application/json", 400},
		{`{} {}`, "application/json", 400},
		{strings.Repeat("x", 5000), "application/json", 400},
		{`email=alice`, "application/x-www-form-urlencoded", 415},
	} {
		r := httptest.NewRequest("POST", testOrigin+"/api/auth/login", strings.NewReader(test.body))
		r.Header.Set("Content-Type", test.contentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("validation got %d, want %d", w.Code, test.status)
		}
	}
	bootstrap(t, h, testOrigin)
	account := sha256.Sum256([]byte("account:alice@example.test"))
	if _, err := db.Exec("UPDATE identity_login_attempts SET attempts=10 WHERE key=?", account[:]); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), db, Config{PublicURL: testOrigin, OwnerCapabilities: s.ownerCapabilities})
	if err != nil {
		t.Fatal(err)
	}
	h = handler(restarted)
	body := map[string]string{"email": "alice@example.test", "password": testPassword}
	if w := send(h, testOrigin, "POST", "/api/auth/login", body, nil); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("rate limit lost after restart")
	}
	if _, err := db.Exec("UPDATE identity_login_attempts SET window_start=?", time.Now().Add(-16*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/login", body, nil); w.Code != 200 {
		t.Fatalf("rate limit did not reset: %d", w.Code)
	}
}

func TestRejectsUnsafePublicOrigins(t *testing.T) {
	for _, origin := range []string{"http://home.example.test", "https://user:pass@home.example.test", "https://home.example.test/path", "https://home.example.test?x=1", ""} {
		if _, err := New(context.Background(), nil, Config{PublicURL: origin}); err == nil {
			t.Fatalf("accepted unsafe public origin: %s", origin)
		}
	}
}

func TestNamesRejectControlCharacters(t *testing.T) {
	for _, name := range []string{"Alice\tAdmin", "Alice\x1b[0m", "Alice\rAdmin", "Alice\x00"} {
		if validText(name, 80) {
			t.Fatalf("unsafe display name %q", name)
		}
	}
	if !validText("María José", 80) {
		t.Fatal("rejected international display name")
	}
}
