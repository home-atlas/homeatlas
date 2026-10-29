package identity

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/database"
)

type sentMail struct{ to, subject, body string }
type testMailer struct {
	messages chan sentMail
	fail     atomic.Bool
	gate     <-chan struct{}
}

func (m *testMailer) Send(ctx context.Context, to, subject, body string) error {
	select {
	case m.messages <- sentMail{to, subject, body}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if m.fail.Load() {
		return errors.New("fake SMTP failure")
	}
	if m.gate != nil {
		select {
		case <-m.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (m *testMailer) next(t *testing.T) sentMail {
	t.Helper()
	select {
	case message := <-m.messages:
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("expected test email")
		return sentMail{}
	}
}
func mailCode(t *testing.T, message sentMail, path string) string {
	t.Helper()
	match := regexp.MustCompile(regexp.QuoteMeta(testOrigin+path) + `#([A-Za-z0-9_-]{43})`).FindStringSubmatch(message.body)
	if len(match) != 2 {
		t.Fatal("mail contains no expected origin/fragment token")
	}
	return match[1]
}
func mailService(t *testing.T) (*Service, http.Handler, *testMailer, *http.Cookie) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	path := filepath.Join(dir, "setup-code")
	if err := os.WriteFile(path, []byte(testSetupCode), 0600); err != nil {
		t.Fatal(err)
	}
	m := &testMailer{messages: make(chan sentMail, 32)}
	s, err := New(context.Background(), db, Config{PublicURL: testOrigin, SetupCodePath: path, Mailer: m, OwnerCapabilities: []string{Manage, "documents.read"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	h := handler(s)
	cookie := bootstrap(t, h, testOrigin)
	return s, h, m, cookie
}
func verifyMailbox(t *testing.T, h http.Handler, m *testMailer, cookie *http.Cookie) string {
	t.Helper()
	w := send(h, testOrigin, "POST", "/api/auth/email/verification", map[string]string{}, cookie)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	code := mailCode(t, m.next(t), "/verify-email")
	if strings.Contains(w.Body.String(), code) {
		t.Fatal("verification token exposed to signed-in requester")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verify", map[string]string{"code": code}, nil); w.Code != 204 || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("verification must not log in", w.Code, w.Body.String())
	}
	return code
}

func TestVerificationRequiresMailboxProofAndIsSingleUse(t *testing.T) {
	s, h, m, cookie := mailService(t)
	state := send(h, testOrigin, "GET", "/api/auth/session", nil, cookie)
	if !strings.Contains(state.Body.String(), `"smtpEnabled":true`) || !strings.Contains(state.Body.String(), `"emailVerified":false`) {
		t.Fatal(state.Body.String())
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verification", nil, nil); w.Code != 401 {
		t.Fatal("verification issuance was public")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verification", nil, cookie); w.Code != 202 {
		t.Fatal(w.Code)
	}
	code := mailCode(t, m.next(t), "/verify-email")
	var hash []byte
	if err := s.db.QueryRow(`SELECT token_hash FROM identity_email_verification`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	want, _ := tokenDigest(code)
	if !bytes.Equal(hash, want) || bytes.Contains(hash, []byte(code)) {
		t.Fatal("raw verification token stored")
	}
	if w := send(h, testOrigin, "GET", "/api/auth/email/verify", nil, nil); w.Code != 405 {
		t.Fatal("GET verified mailbox")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verify", map[string]string{"code": testSetupCode}, nil); w.Code != 410 {
		t.Fatal("wrong-purpose token accepted")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verify", map[string]string{"code": code}, nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verify", map[string]string{"code": code}, nil); w.Code != 410 {
		t.Fatal("verification replay accepted")
	}
	state = send(h, testOrigin, "GET", "/api/auth/session", nil, cookie)
	if !strings.Contains(state.Body.String(), `"emailVerified":true`) {
		t.Fatal("verification not reflected in session")
	}
}

func TestVerificationReissueExpiryAndBindingToAddress(t *testing.T) {
	s, h, m, cookie := mailService(t)
	request := func() string {
		t.Helper()
		if w := send(h, testOrigin, "POST", "/api/auth/email/verification", nil, cookie); w.Code != 202 {
			t.Fatal(w.Code)
		}
		return mailCode(t, m.next(t), "/verify-email")
	}
	old := request()
	latest := request()
	if w := send(h, testOrigin, "POST", "/api/auth/email/verify", map[string]string{"code": old}, nil); w.Code != 410 {
		t.Fatal("old verification survived reissue")
	}
	if _, err := s.db.Exec(`UPDATE identity_email_verification SET expires_at=0`); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verify", map[string]string{"code": latest}, nil); w.Code != 410 {
		t.Fatal("expired verification accepted")
	}
	latest = request()
	if _, err := s.db.Exec(`UPDATE identity_users SET email='changed@example.test'`); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verify", map[string]string{"code": latest}, nil); w.Code != 410 {
		t.Fatal("verification moved to a different address")
	}
}

func TestEmailRecoveryOnlyForVerifiedAccountsWithoutEnumerating(t *testing.T) {
	s, h, m, cookie := mailService(t)
	for _, email := range []string{"unknown@example.test", "alice@example.test"} {
		if err := s.sendRecoveryEmail(context.Background(), email); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-m.messages:
		t.Fatal("mail sent to unknown or unverified account")
	default:
	}
	verifyMailbox(t, h, m, cookie)
	var expected string
	for _, email := range []string{"unknown@example.test", "alice@example.test"} {
		w := send(h, testOrigin, "POST", "/api/auth/recovery/request", map[string]string{"email": email}, nil)
		if w.Code != 202 || w.Header().Get("Set-Cookie") != "" {
			t.Fatal(w.Code, w.Body.String())
		}
		if expected != "" && w.Body.String() != expected {
			t.Fatal("recovery enumerates accounts")
		}
		expected = w.Body.String()
	}
	message := m.next(t)
	if message.to != "alice@example.test" {
		t.Fatal("wrong recovery recipient")
	}
	code := mailCode(t, message, "/forgot-password")
	var stored []byte
	s.db.QueryRow(`SELECT token_hash FROM identity_mail_recovery`).Scan(&stored)
	want, _ := tokenDigest(code)
	if !bytes.Equal(stored, want) {
		t.Fatal("mail recovery token not hashed")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/email/verify", map[string]string{"code": code}, nil); w.Code != 410 {
		t.Fatal("recovery token verified mailbox")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": "a recovered mailbox passphrase"}, nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(h, testOrigin, "GET", "/api/probe", nil, cookie); w.Code != 401 {
		t.Fatal("email recovery left session alive")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": testPassword}, nil); w.Code != 410 {
		t.Fatal("email recovery reused")
	}
}

func TestMailRequestsDoNotInvalidateLocalRecovery(t *testing.T) {
	s, h, m, cookie := mailService(t)
	verifyMailbox(t, h, m, cookie)
	local := issueRecovery(t, s.db)
	request := func() string {
		t.Helper()
		if w := send(h, testOrigin, "POST", "/api/auth/recovery/request", map[string]string{"email": "alice@example.test"}, nil); w.Code != 202 {
			t.Fatal(w.Code)
		}
		return mailCode(t, m.next(t), "/forgot-password")
	}
	first := request()
	latest := request()
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": first, "password": testPassword}, nil); w.Code != 410 {
		t.Fatal("old emailed recovery survived reissue")
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": local, "password": testPassword}, nil); w.Code != 204 {
		t.Fatal("email requests invalidated local recovery", w.Code, w.Body.String())
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": latest, "password": testPassword}, nil); w.Code != 410 {
		t.Fatal("local recovery left old emailed code usable")
	}
}

func TestSMTPFailurePreservesManualInvitationAndDoesNotVerifyNewAccount(t *testing.T) {
	_, h, m, cookie := mailService(t)
	m.fail.Store(true)
	w := send(h, testOrigin, "POST", "/api/household/invitations", map[string]any{"email": "invitee@example.test", "role": "member", "permissions": []string{}, "sendEmail": true}, cookie)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"emailStatus":"failed"`) || !strings.Contains(w.Body.String(), `"url":`) {
		t.Fatal("SMTP failure discarded manual invitation", w.Code, w.Body.String())
	}
	message := m.next(t)
	code := mailCode(t, message, "/join")
	if w := send(h, testOrigin, "POST", "/api/invitations/accept", map[string]string{"token": code, "name": "New member", "password": testPassword}, nil); w.Code != 201 || !strings.Contains(w.Body.String(), `"emailVerified":false`) {
		t.Fatal("invitation must not prove mailbox ownership", w.Code, w.Body.String())
	}
}

func TestDisabledSMTPKeepsLocalAccess(t *testing.T) {
	_, h, _ := newService(t, testOrigin)
	cookie := bootstrap(t, h, testOrigin)
	for _, path := range []string{"/api/auth/email/verification", "/api/auth/recovery/request"} {
		if w := send(h, testOrigin, "POST", path, map[string]string{}, cookie); w.Code != 503 || !strings.Contains(w.Body.String(), "smtp_disabled") {
			t.Fatal("disabled SMTP accepted send", w.Code)
		}
	}
	if w := send(h, testOrigin, "GET", "/api/probe", nil, cookie); w.Code != 200 {
		t.Fatal("SMTP disabled login")
	}
}

func TestRecoveryRequestDoesNotWaitForSMTP(t *testing.T) {
	_, h, m, cookie := mailService(t)
	verifyMailbox(t, h, m, cookie)
	gate := make(chan struct{})
	m.gate = gate
	defer close(gate)
	done := make(chan int, 1)
	go func() {
		done <- send(h, testOrigin, "POST", "/api/auth/recovery/request", map[string]string{"email": "alice@example.test"}, nil).Code
	}()
	select {
	case status := <-done:
		if status != 202 {
			t.Fatal(status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("public request waited for SMTP")
	}
	m.next(t)
}

func TestEmailRecoveryRejectsExpiryAndLostMailboxProof(t *testing.T) {
	s, h, m, cookie := mailService(t)
	verifyMailbox(t, h, m, cookie)
	issue := func() string {
		t.Helper()
		if err := s.sendRecoveryEmail(context.Background(), "alice@example.test"); err != nil {
			t.Fatal(err)
		}
		return mailCode(t, m.next(t), "/forgot-password")
	}
	code := issue()
	if _, err := s.db.Exec(`UPDATE identity_mail_recovery SET expires_at=0`); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": testPassword}, nil); w.Code != 410 {
		t.Fatal("expired email recovery accepted")
	}
	code = issue()
	if _, err := s.db.Exec(`DELETE FROM identity_verified_emails`); err != nil {
		t.Fatal(err)
	}
	if w := send(h, testOrigin, "POST", "/api/auth/recover", map[string]string{"code": code, "password": testPassword}, nil); w.Code != 410 {
		t.Fatal("recovery accepted without current verified mailbox")
	}
	if w := send(h, testOrigin, "GET", "/api/probe", nil, cookie); w.Code != 200 {
		t.Fatal("invalid recovery revoked local session")
	}
}
