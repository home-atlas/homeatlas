package identity

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/database"
)

func TestSetupRequiresPrivateCodeAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	path := filepath.Join(dir, "setup-code")
	config := Config{PublicURL: testOrigin, SetupCodePath: path}
	s, err := New(context.Background(), db, config)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimSpace(string(raw))
	if _, ok := tokenDigest(code); !ok || code == testSetupCode {
		t.Fatal("setup secret was not generated")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("setup secret is readable by other users")
	}
	for _, code := range []string{"", "wrong", testSetupCode} {
		body := setupBody()
		body["code"] = code
		w := send(handler(s), testOrigin, "POST", "/api/setup", body, nil)
		if w.Code != 403 || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("unauthorized setup: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM identity_users`).Scan(&count)
	if count != 0 {
		t.Fatal("code failure created an account")
	}
	state := send(handler(s), testOrigin, "GET", "/api/auth/session", nil, nil)
	if strings.Contains(state.Body.String(), code) {
		t.Fatal("setup code exposed over HTTP")
	}
	s, err = New(context.Background(), db, config)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(raw, after) {
		t.Fatal("restart changed the setup code")
	}
	body := setupBody()
	body["code"] = " " + code + "\n"
	if w := send(handler(s), testOrigin, "POST", "/api/setup", body, nil); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("consumed setup file remains")
	}
	s, err = New(context.Background(), db, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("restart reopened setup")
	}
	if w := send(handler(s), testOrigin, "POST", "/api/setup", body, nil); w.Code != 409 {
		t.Fatal("setup code replay succeeded")
	}
}

func TestSetupRejectsUnsafeCodeFiles(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "damaged", "missing configuration"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			path := filepath.Join(dir, "setup-code")
			switch kind {
			case "public":
				if err := os.WriteFile(path, []byte(testSetupCode), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(dir, "other")
				if err := os.WriteFile(target, []byte(testSetupCode), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "damaged":
				if err := os.WriteFile(path, []byte(""), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing configuration":
				path = ""
			}
			if _, err := New(context.Background(), db, Config{PublicURL: testOrigin, SetupCodePath: path}); err == nil {
				t.Fatal("unsafe setup file accepted")
			}
		})
	}
}

func issueRecovery(t *testing.T, db *sql.DB) string {
	t.Helper()
	code, expiry, err := IssueRecovery(context.Background(), db, " Alice@Example.test ")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tokenDigest(code); !ok || time.Until(expiry) > recoveryLifetime || time.Until(expiry) < 14*time.Minute {
		t.Fatal("invalid recovery code or expiry")
	}
	return code
}

func TestLocalRecoveryRevokesSessionsAndDoesNotChangeAccess(t *testing.T) {
	s, h, db := newService(t, testOrigin)
	cookie := bootstrap(t, h, testOrigin)
	otherSession := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"email": "alice@example.test", "password": testPassword}, nil).Result().Cookies()[0]
	// A second household/session ensures revocation is account-wide.
	if _, err := db.Exec(`INSERT INTO identity_households VALUES('second','Other','UTC');
 INSERT INTO identity_memberships SELECT id,'second','member' FROM identity_users;
 INSERT INTO identity_sessions SELECT randomblob(32),id,'second',9999999999,9999999999 FROM identity_users;
 INSERT INTO identity_invitations VALUES('pending',(SELECT id FROM identity_households WHERE id!='second'),'guest@example.test','guest','[]',randomblob(32),(SELECT id FROM identity_users),'member',1,9999999999,0,0);`); err != nil {
		t.Fatal(err)
	}
	code := issueRecovery(t, db)
	var digest []byte
	db.QueryRow(`SELECT token_hash FROM identity_recovery`).Scan(&digest)
	want, _ := tokenDigest(code)
	if !bytes.Equal(digest, want) || bytes.Contains(digest, []byte(code)) {
		t.Fatal("raw recovery token stored")
	}
	if got := send(h, testOrigin, "GET", "/api/probe", nil, cookie); got.Code != 200 {
		t.Fatal("issuing recovery revoked access before completion")
	}
	// Recovery is persistent across server restarts.
	restarted, err := New(context.Background(), db, Config{PublicURL: testOrigin, OwnerCapabilities: s.ownerCapabilities})
	if err != nil {
		t.Fatal(err)
	}
	h = handler(restarted)
	const nextPassword = "a different private passphrase"
	w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": nextPassword}, cookie)
	if w.Code != 204 || w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, table := range []string{"identity_recovery", "identity_sessions"} {
		var count int
		db.QueryRow("SELECT count(*) FROM " + table).Scan(&count)
		if count != 0 {
			t.Fatal("recovery left active tokens in", table)
		}
	}
	var role string
	db.QueryRow(`SELECT role FROM identity_memberships WHERE household_id!='second'`).Scan(&role)
	if role != "owner" {
		t.Fatal("recovery changed the role")
	}
	var permissions int
	db.QueryRow(`SELECT count(*) FROM identity_permissions`).Scan(&permissions)
	if permissions != 1 {
		t.Fatal("recovery changed permissions")
	}
	var revoked int
	db.QueryRow(`SELECT revoked_at FROM identity_invitations WHERE id='pending'`).Scan(&revoked)
	if revoked == 0 {
		t.Fatal("pending issued invitation survived recovery")
	}
	for _, stale := range []*http.Cookie{cookie, otherSession} {
		if w := send(h, testOrigin, "GET", "/api/probe", nil, stale); w.Code != 401 {
			t.Fatal("old session survived recovery")
		}
	}
	if w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"email": "alice@example.test", "password": testPassword}, nil); w.Code != 401 {
		t.Fatal("old password accepted")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/login", map[string]string{"email": "alice@example.test", "password": nextPassword}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": testPassword}, nil); w.Code != 410 {
		t.Fatal("recovery code replayed")
	}
}

func TestRecoveryValidationReissueExpiryAndSingleUse(t *testing.T) {
	_, h, db := newService(t, testOrigin)
	bootstrap(t, h, testOrigin)
	if _, _, err := IssueRecovery(context.Background(), db, "unknown@example.test"); err == nil {
		t.Fatal("recovery created a missing account")
	}
	first := issueRecovery(t, db)
	latest := issueRecovery(t, db)
	for _, code := range []string{"", "invalid", first, testSetupCode} {
		if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": testPassword}, nil); w.Code != 410 {
			t.Fatal("invalid/replaced code accepted", w.Code)
		}
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": latest, "password": "short"}, nil); w.Code != 400 {
		t.Fatal("weak recovery password accepted")
	}
	if _, err := db.Exec(`UPDATE identity_recovery SET expires_at=0`); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": latest, "password": testPassword}, nil); w.Code != 410 {
		t.Fatal("expired recovery code accepted")
	}
	latest = issueRecovery(t, db)
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			statuses <- send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": latest, "password": testPassword}, nil).Code
		})
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[204] != 1 || counts[410] != 1 {
		t.Fatal("concurrent recovery consumed twice", counts)
	}
}

func TestRecoveryRollbackRateLimitAndClosedRegistration(t *testing.T) {
	_, h, db := newService(t, testOrigin)
	cookie := bootstrap(t, h, testOrigin)
	code := issueRecovery(t, db)
	if _, err := db.Exec(`CREATE TRIGGER fail_recovery BEFORE DELETE ON identity_sessions BEGIN SELECT RAISE(ABORT,'test rollback'); END;`); err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"code": code, "password": "another long recovery passphrase"}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", body, nil); w.Code != 503 {
		t.Fatal("recovery failure ignored")
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM identity_recovery`).Scan(&count)
	if count != 1 {
		t.Fatal("failed recovery consumed code")
	}
	var stored string
	if err := db.QueryRow(`SELECT password_hash FROM identity_users`).Scan(&stored); err != nil || !verifyPassword(testPassword, stored) {
		t.Fatal("failed recovery changed the password", err)
	}
	if got := send(h, testOrigin, "GET", "/api/probe", nil, cookie); got.Code != 200 {
		t.Fatal("failed recovery revoked session")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_recovery`); err != nil {
		t.Fatal(err)
	}
	for range 9 {
		send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": "short"}, nil)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", body, nil); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("recovery not rate limited")
	}
	for _, path := range []string{"/api/auth/register"} {
		if w := send(h, testOrigin, "POST", path, map[string]string{"email": "alice@example.test"}, nil); w.Code != 404 {
			t.Fatal("public registration/recovery issuance exposed")
		}
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recovery/request", map[string]string{"email": "alice@example.test"}, nil); w.Code != 503 {
		t.Fatal("email recovery must stay disabled without SMTP")
	}
}

func TestRecoveryDoesNotReactivateRemovedAccounts(t *testing.T) {
	_, h, db := newService(t, testOrigin)
	bootstrap(t, h, testOrigin)
	code := issueRecovery(t, db)
	if _, err := db.Exec(`DELETE FROM identity_memberships`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := IssueRecovery(context.Background(), db, "alice@example.test"); err == nil {
		t.Fatal("removed account received a recovery code")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": testPassword}, nil); w.Code != 410 {
		t.Fatal("recovery restored a removed account")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM identity_memberships`).Scan(&count); err != nil || count != 0 {
		t.Fatal("recovery recreated membership", err)
	}
}
