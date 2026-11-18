package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/identity/application"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
)

func accountSessions(t *testing.T, h http.Handler, cookie *http.Cookie) ([]domain.AccountSession, bool) {
	t.Helper()
	w := send(h, testOrigin, "GET", "/api/auth/sessions", nil, cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Sessions []domain.AccountSession `json:"sessions"`
		More     bool                    `json:"more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Sessions, out.More
}
func anotherAccountSession(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"identifier": "alice@example.test", "password": testPassword}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

func TestOwnAccountProfileAndSessions(t *testing.T) {
	_, h, db := newService(t, testOrigin)
	current := bootstrap(t, h, testOrigin)
	other := anotherAccountSession(t, h)
	var user, home string
	if err := db.QueryRow("SELECT user_id,household_id FROM identity_sessions LIMIT 1").Scan(&user, &home); err != nil {
		t.Fatal(err)
	}
	// A second household of the same account must remain outside this list/revocation.
	if _, err := db.Exec("INSERT INTO identity_households VALUES('another-home','Other home','UTC'); INSERT INTO identity_memberships VALUES(?,'another-home','owner')", user); err != nil {
		t.Fatal(err)
	}
	foreign := &http.Cookie{Name: current.Name, Value: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))}
	foreignHash := sha256.Sum256([]byte(foreign.Value))
	now := time.Now().Unix()
	if _, err := db.Exec("INSERT INTO identity_sessions VALUES(?,?,'another-home',?,?)", foreignHash[:], user, now+3600, now); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/auth/profile", "/api/auth/sessions"} {
		if w := send(h, testOrigin, "GET", path, nil, nil); w.Code != 401 {
			t.Fatal("anonymous", path, w.Code)
		}
	}
	items, more := accountSessions(t, h, current)
	if len(items) != 2 || more || !items[0].Current {
		t.Fatal("session scope/current", items, more)
	}
	w := send(h, testOrigin, "GET", "/api/auth/sessions", nil, current)
	for _, cookie := range []*http.Cookie{current, other, foreign} {
		hash := sha256.Sum256([]byte(cookie.Value))
		for _, secret := range []string{cookie.Value, base64.StdEncoding.EncodeToString(hash[:]), fmt.Sprintf("%x", hash)} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("session credential exposed")
			}
		}
	}
	if w := send(h, testOrigin, "PATCH", "/api/auth/profile", map[string]string{"name": "Alicia", "role": "owner"}, current); w.Code != 400 {
		t.Fatal("extra profile field", w.Code)
	}
	for _, name := range []string{"", strings.Repeat("x", 81), "bad\nname"} {
		if w := send(h, testOrigin, "PATCH", "/api/auth/profile", map[string]string{"name": name}, current); w.Code != 400 {
			t.Fatal("invalid name", w.Code)
		}
	}
	if w := send(h, "https://untrusted.test", "PATCH", "/api/auth/profile", map[string]string{"name": "Wrong"}, current); w.Code != 421 && w.Code != 403 {
		t.Fatal("cross origin", w.Code)
	}
	if w := send(h, testOrigin, "PATCH", "/api/auth/profile", map[string]string{"name": "  Alicia  "}, current); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Alicia"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(h, testOrigin, "GET", "/api/auth/session", nil, other); !strings.Contains(w.Body.String(), `"name":"Alicia"`) {
		t.Fatal("profile not persisted", w.Body.String())
	}
	foreignID := domain.SessionHandle(foreignHash[:])
	if w := send(h, testOrigin, "DELETE", "/api/auth/sessions/"+foreignID, nil, current); w.Code != 404 {
		t.Fatal("foreign session revoked", w.Code)
	}
	for _, item := range items {
		if !item.Current {
			if w := send(h, testOrigin, "DELETE", "/api/auth/sessions/"+item.ID, nil, current); w.Code != 204 {
				t.Fatal(w.Code)
			}
		}
	}
	if w := send(h, testOrigin, "GET", "/api/auth/profile", nil, other); w.Code != 401 {
		t.Fatal("revoked session survived", w.Code)
	}
	other = anotherAccountSession(t, h)
	if w := send(h, testOrigin, "POST", "/api/auth/sessions/revoke-others", map[string]any{}, current); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(h, testOrigin, "GET", "/api/auth/profile", nil, other); w.Code != 401 {
		t.Fatal("other session survived", w.Code)
	}
	if w := send(h, testOrigin, "GET", "/api/auth/profile", nil, foreign); w.Code != 200 {
		t.Fatal("other household revoked", w.Code)
	}
	items, _ = accountSessions(t, h, current)
	if len(items) != 1 || !items[0].Current {
		t.Fatal(items)
	}
	forged := &http.Cookie{Name: current.Name, Value: items[0].ID}
	if w := send(h, testOrigin, "GET", "/api/auth/profile", nil, forged); w.Code != 401 {
		t.Fatal("management handle authenticates", w.Code)
	}
	w = send(h, testOrigin, "DELETE", "/api/auth/sessions/"+items[0].ID, nil, current)
	if w.Code != 204 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("current session cookie not ended", w.Code)
	}
}

func TestOwnAccountMutationRechecksSessionAndRollsBack(t *testing.T) {
	s, h, db := newService(t, testOrigin)
	cookie := bootstrap(t, h, testOrigin)
	other := anotherAccountSession(t, h)
	if _, err := db.Exec("CREATE TRIGGER reject_profile BEFORE UPDATE OF name ON identity_users BEGIN SELECT RAISE(ABORT,'fixture'); END;"); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "PATCH", "/api/auth/profile", map[string]string{"name": "Rejected"}, cookie); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := db.Exec("DROP TRIGGER reject_profile; CREATE TRIGGER reject_revoke BEFORE DELETE ON identity_sessions BEGIN SELECT RAISE(ABORT,'fixture'); END;"); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/sessions/revoke-others", map[string]any{}, cookie); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if _, err := db.Exec("DROP TRIGGER reject_revoke"); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "GET", "/api/auth/profile", nil, other); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Alice"`) {
		t.Fatal("failed mutation escaped transaction", w.Code, w.Body.String())
	}
	ctx, err := s.Authenticate(context.Background(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	if _, err = db.Exec("DELETE FROM identity_sessions WHERE token_hash=?", hash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProfile(ctx, application.Client{HasSession: true, SessionToken: cookie.Value}, application.UpdateProfileInput{Name: "Stale request"}); err == nil {
		t.Fatal("stale authentication mutated profile")
	}
	otherHash := sha256.Sum256([]byte(other.Value))
	if _, err = db.Exec("UPDATE identity_memberships SET role='member'; DELETE FROM identity_permissions WHERE capability LIKE 'household.%'; INSERT INTO identity_pin_sessions VALUES(?)", otherHash[:]); err != nil {
		t.Fatal(err)
	}
	ctx, err = s.Authenticate(context.Background(), other.Value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE identity_memberships SET role='admin'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProfile(ctx, application.Client{HasSession: true, SessionToken: other.Value}, application.UpdateProfileInput{Name: "Stale PIN"}); err == nil {
		t.Fatal("PIN session stayed usable after administrative promotion")
	}
}

func TestOwnAccountSessionListIsBounded(t *testing.T) {
	_, h, db := newService(t, testOrigin)
	cookie := bootstrap(t, h, testOrigin)
	var user, home string
	if err := db.QueryRow("SELECT user_id,household_id FROM identity_sessions LIMIT 1").Scan(&user, &home); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for i := range 105 {
		hash := sha256.Sum256([]byte(fmt.Sprintf("fixture-session-%d", i)))
		if _, err := db.Exec("INSERT INTO identity_sessions VALUES(?,?,?,?,?)", hash[:], user, home, now+3600, now+1); err != nil {
			t.Fatal(err)
		}
	}
	items, more := accountSessions(t, h, cookie)
	if len(items) != 100 || !more || !items[0].Current {
		t.Fatal("unbounded or missing current session", len(items), more)
	}
}
