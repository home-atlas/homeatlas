package identity

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"crypto/rand"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

func (s *Service) initialized(r *http.Request) (bool, error) {
	var initialized bool
	err := s.db.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM identity_installation)").Scan(&initialized)
	return initialized, err
}

func (s *Service) session(w http.ResponseWriter, r *http.Request) {
	ready, err := s.initialized(r)
	if err != nil {
		fail(w, 503, "session_unavailable")
		return
	}
	current, _ := r.Context().Value(sessionKey{}).(*Session)
	modules.JSON(w, 200, State{SetupRequired: !ready, Session: current, SMTPEnabled: s.mailer != nil})
}

type setupInput struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	HouseholdName string `json:"householdName"`
	Timezone      string `json:"timezone"`
}

func (s *Service) setup(w http.ResponseWriter, r *http.Request) {
	ready, err := s.initialized(r)
	if err != nil {
		fail(w, 503, "setup_unavailable")
		return
	}
	if ready {
		fail(w, 409, "setup_completed")
		return
	}
	var input setupInput
	if !decode(w, r, &input) {
		return
	}
	if !s.limit(w, r, "setup") {
		return
	}
	if !s.validSetupCode(input.Code) {
		fail(w, 403, "invalid_setup_code")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.HouseholdName = strings.TrimSpace(input.HouseholdName)
	email, ok := optionalEmail(input.Email)
	username, userOK := normalizeUsername(input.Username)
	if input.Username == "" && email != "" {
		username, userOK = "u-"+strings.ToLower(rand.Text()), true
	}
	if !validTimezone(input.Timezone) {
		fail(w, 400, "invalid_setup")
		return
	}
	if !ok || !userOK || !validText(input.Name, 80) || !validText(input.HouseholdName, 100) || !validNewPassword(input.Password) {
		fail(w, 400, "invalid_setup")
		return
	}
	if !s.acquire(w) {
		return
	}
	defer func() { <-s.work }()
	if !s.limit(w, r, email) {
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		fail(w, 500, "setup_unavailable")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "setup_unavailable")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), "INSERT INTO identity_installation(singleton) VALUES (1) ON CONFLICT DO NOTHING")
	if err != nil {
		fail(w, 500, "setup_unavailable")
		return
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		fail(w, 409, "setup_completed")
		return
	}
	userID, householdID := rand.Text(), rand.Text()
	if input.Username == "" {
		username = "u-" + strings.ToLower(userID)
	}
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO identity_users (id, name, email, password_hash, username) VALUES (?, ?, ?, ?, ?)", userID, input.Name, nullableEmail(email), hash, username); err != nil {
		fail(w, 500, "setup_unavailable")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO identity_households (id, name, timezone) VALUES (?, ?, ?)", householdID, input.HouseholdName, input.Timezone); err != nil {
		fail(w, 500, "setup_unavailable")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO identity_memberships (user_id, household_id, role) VALUES (?, ?, 'owner')", userID, householdID); err != nil {
		fail(w, 500, "setup_unavailable")
		return
	}
	for _, capability := range s.ownerCapabilities {
		if _, err := tx.ExecContext(r.Context(), "INSERT INTO identity_permissions (user_id, household_id, capability) VALUES (?, ?, ?)", userID, householdID, capability); err != nil {
			fail(w, 500, "setup_unavailable")
			return
		}
	}
	token, err := s.createSession(r.Context(), tx, r, userID, householdID)
	if err != nil {
		fail(w, 500, "setup_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "setup_unavailable")
		return
	}
	s.removeSetupCode()
	s.respondSession(w, r, token, 201)
}

type loginInput struct {
	Identifier string `json:"identifier"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var input loginInput
	if !decode(w, r, &input) {
		return
	}
	identifier := input.Identifier
	if identifier == "" {
		identifier = input.Email
	}
	email, ok := normalizeLogin(identifier)
	if !ok || len(input.Password) == 0 || len(input.Password) > 512 || !utf8.ValidString(input.Password) {
		fail(w, 401, "invalid_credentials")
		return
	}
	if !s.acquire(w) {
		return
	}
	defer func() { <-s.work }()
	var userID, storedHash, account string
	err := s.db.QueryRowContext(r.Context(), "SELECT id, password_hash, COALESCE(email,username) FROM identity_users WHERE email = ? OR username = ?", email, email).Scan(&userID, &storedHash, &account)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "login_unavailable")
		return
	}
	if account == "" {
		account = email
	}
	if !s.limit(w, r, account) {
		return
	}
	if errors.Is(err, sql.ErrNoRows) || !validPasswordHash(storedHash) {
		storedHash = s.dummyHash
	}
	valid := verifyPassword(input.Password, storedHash)
	if !valid || userID == "" {
		fail(w, 401, "invalid_credentials")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "login_unavailable")
		return
	}
	defer tx.Rollback()
	var householdID string
	// shortcut: select the first membership until the multi-household selector is implemented.
	// Recheck credentials under the write transaction: recovery may have changed them during hashing.
	err = tx.QueryRowContext(r.Context(), `SELECT m.household_id FROM identity_memberships m JOIN identity_users u ON u.id=m.user_id
 WHERE m.user_id=? AND u.password_hash=? ORDER BY m.household_id LIMIT 1`, userID, storedHash).Scan(&householdID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "invalid_credentials")
		return
	}
	if err != nil {
		fail(w, 503, "login_unavailable")
		return
	}
	token, err := s.createSession(r.Context(), tx, r, userID, householdID)
	if err != nil {
		fail(w, 503, "login_unavailable")
		return
	}
	accountKey := sha256.Sum256([]byte("account:" + account))
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM identity_login_attempts WHERE key = ?", accountKey[:]); err != nil {
		fail(w, 503, "login_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "login_unavailable")
		return
	}
	s.respondSession(w, r, token, 200)
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(s.cookieName); err == nil {
		digest := sha256.Sum256([]byte(cookie.Value))
		if _, err := s.db.ExecContext(r.Context(), "DELETE FROM identity_sessions WHERE token_hash = ?", digest[:]); err != nil {
			fail(w, 503, "logout_unavailable")
			return
		}
	}
	s.clearCookie(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(w, 415, "json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(w, 400, "invalid_request")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, 400, "invalid_request")
		return false
	}
	return true
}

func normalizeEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	return email, err == nil && address.Address == email && len(email) <= 254
}

func validText(value string, max int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func (s *Service) acquire(w http.ResponseWriter) bool {
	select {
	case s.work <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		fail(w, 429, "too_many_attempts")
		return false
	}
}

// Counters persist across restarts and never trust client-supplied forwarded IPs.
func (s *Service) limit(w http.ResponseWriter, r *http.Request, email string) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "login_unavailable")
		return false
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	window := now - int64(attemptWindow.Seconds())
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM identity_login_attempts WHERE window_start <= ?", window); err != nil {
		fail(w, 503, "login_unavailable")
		return false
	}
	for _, rule := range []struct {
		key string
		max int
	}{{"ip:" + ip, 50}, {"account:" + email, 10}} {
		digest := sha256.Sum256([]byte(rule.key))
		if _, err := tx.ExecContext(r.Context(), "INSERT INTO identity_login_attempts (key, window_start, attempts) VALUES (?, ?, 0) ON CONFLICT DO NOTHING", digest[:], now); err != nil {
			fail(w, 503, "login_unavailable")
			return false
		}
		result, err := tx.ExecContext(r.Context(), "UPDATE identity_login_attempts SET attempts = attempts + 1 WHERE key = ? AND attempts < ?", digest[:], rule.max)
		if err != nil {
			fail(w, 503, "login_unavailable")
			return false
		}
		if count, err := result.RowsAffected(); err != nil || count == 0 {
			w.Header().Set("Retry-After", "900")
			fail(w, 429, "too_many_attempts")
			return false
		}
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "login_unavailable")
		return false
	}
	return true
}
