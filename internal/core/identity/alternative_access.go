package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,39}$`)
var pinPattern = regexp.MustCompile(`^[0-9]{6,12}$`)

const pinDeviceLifetime = 30 * 24 * time.Hour

func normalizeUsername(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	return value, usernamePattern.MatchString(value)
}
func normalizeLogin(value string) (string, bool) {
	if strings.Contains(value, "@") {
		return normalizeEmail(value)
	}
	return normalizeUsername(value)
}
func optionalEmail(value string) (string, bool) {
	if strings.TrimSpace(value) == "" {
		return "", true
	}
	return normalizeEmail(value)
}
func nullableEmail(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func (s *Service) pinCookieName() string {
	if s.secure {
		return "__Host-homeatlas_pin_device"
	}
	return "homeatlas_pin_device"
}
func (s *Service) setPINCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: s.pinCookieName(), Value: token, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: int(pinDeviceLifetime.Seconds()), Expires: time.Now().Add(pinDeviceLifetime)})
}
func (s *Service) mountAlternativeAccess(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/household/assisted-members", s.createAssistedMember)
	mux.HandleFunc("POST /api/household/members/{user}/enrollment", s.renewAssistedEnrollment)
	mux.HandleFunc("POST /api/assisted/inspect", s.inspectAssistedEnrollment)
	mux.HandleFunc("POST /api/assisted/complete", s.completeAssistedEnrollment)
	mux.HandleFunc("GET /api/auth/pin", s.pinStatus)
	mux.HandleFunc("POST /api/auth/pin/login", s.pinLogin)
	mux.HandleFunc("POST /api/auth/pin/device", s.registerPINDevice)
	mux.HandleFunc("GET /api/auth/access", s.accessSettings)
	mux.HandleFunc("PATCH /api/auth/access", s.updateUsername)
	mux.HandleFunc("DELETE /api/auth/pin/devices/{id}", s.revokePINDevice)
}

// Fresh password proof is rechecked in the committing transaction, including session validity.
func (s *Service) passwordProof(w http.ResponseWriter, r *http.Request, password string) (string, string, bool) {
	p, ok := access.Current(r.Context())
	if !ok {
		fail(w, 401, "authentication_required")
		return "", "", false
	}
	if !s.limit(w, r, "access:"+p.UserID) || !s.acquire(w) {
		return "", "", false
	}
	defer func() { <-s.work }()
	var stored string
	if err := s.db.QueryRowContext(r.Context(), "SELECT password_hash FROM identity_users WHERE id=?", p.UserID).Scan(&stored); err != nil {
		fail(w, 503, "access_unavailable")
		return "", "", false
	}
	if len(password) > 512 || !verifyPassword(password, stored) {
		fail(w, 403, "password_confirmation_required")
		return "", "", false
	}
	cookie, err := r.Cookie(s.cookieName)
	if err != nil {
		fail(w, 401, "authentication_required")
		return "", "", false
	}
	return stored, cookie.Value, true
}
func (s *Service) proofCurrent(r *http.Request, tx *sql.Tx, stored, token string) bool {
	p, ok := access.Current(r.Context())
	if !ok {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	now := time.Now().Unix()
	var valid bool
	err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_sessions s JOIN identity_users u ON u.id=s.user_id JOIN identity_member_ids m ON m.user_id=s.user_id AND m.household_id=s.household_id WHERE s.token_hash=? AND s.user_id=? AND s.household_id=? AND s.expires_at>? AND s.last_seen>? AND u.password_hash=? AND m.id=?)`, digest[:], p.UserID, p.HouseholdID, now, now-int64(sessionIdle.Seconds()), stored, p.MembershipID).Scan(&valid)
	return err == nil && valid
}
func pinRole(role string) bool { return role == "member" || role == "restricted" || role == "guest" }

type assistedInput struct {
	Name        string   `json:"name"`
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	Password    string   `json:"password"`
}

func (s *Service) createAssistedMember(w http.ResponseWriter, r *http.Request) {
	var in assistedInput
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	username, valid := normalizeUsername(in.Username)
	email, emailOK := optionalEmail(in.Email)
	if !valid || !emailOK || !validText(in.Name, 80) || !pinRole(in.Role) {
		fail(w, 400, "invalid_assisted_member")
		return
	}
	proof, sessionToken, ok := s.passwordProof(w, r, in.Password)
	if !ok {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer tx.Rollback()
	actor, ok := s.actor(w, r, tx)
	if !ok {
		return
	}
	if !s.proofCurrent(r, tx, proof, sessionToken) {
		fail(w, 401, "authentication_required")
		return
	}
	permissions, ok := s.grant(actor, in.Role, in.Permissions)
	if !ok {
		fail(w, 403, "invalid_grant")
		return
	}
	// PIN accounts never receive installation or household administration capabilities.
	for _, p := range permissions {
		if strings.HasPrefix(p, "household.") {
			fail(w, 403, "invalid_grant")
			return
		}
	}
	var exists bool
	err = tx.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM identity_users WHERE username=? OR email=?)", username, nullableEmail(email)).Scan(&exists)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	if exists {
		fail(w, 409, "account_identifier_taken")
		return
	}
	user := rand.Text()
	p, _ := access.Current(r.Context())
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO identity_users(id,name,email,password_hash,username,assisted) VALUES(?,?,?,'!',?,1)", user, in.Name, nullableEmail(email), username); err != nil {
		fail(w, 409, "account_identifier_taken")
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO identity_memberships(user_id,household_id,role) VALUES(?,?,?)", user, p.HouseholdID, in.Role); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	if err = setPermissions(r.Context(), tx, p.HouseholdID, user, permissions); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	s.issueAssistedEnrollment(w, r, tx, user, actor, 201)
}
func (s *Service) issueAssistedEnrollment(w http.ResponseWriter, r *http.Request, tx *sql.Tx, user string, actor access.Member, status int) {
	p, _ := access.Current(r.Context())
	token := randomCode()
	digest, _ := tokenDigest(token)
	expires := time.Now().Add(recoveryLifetime)
	_, err := tx.ExecContext(r.Context(), `INSERT INTO identity_assisted_enrollments(user_id,household_id,token_hash,issuer_id,issuer_member_id,expires_at) VALUES(?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET household_id=excluded.household_id,token_hash=excluded.token_hash,issuer_id=excluded.issuer_id,issuer_member_id=excluded.issuer_member_id,expires_at=excluded.expires_at`, user, p.HouseholdID, digest, actor.ID, actor.MembershipID, expires.Unix())
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO identity_access_events(id,user_id,household_id,actor_id,action,created_at) VALUES(?,?,?,?,'enrollment_issued',?)`, rand.Text(), user, p.HouseholdID, actor.ID, time.Now().Unix())
	}
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	modules.JSON(w, status, map[string]any{"url": s.origin.String() + "/activate-access#" + token, "expiresAt": expires, "userId": user})
}
func (s *Service) renewAssistedEnrollment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	proof, sessionToken, ok := s.passwordProof(w, r, in.Password)
	if !ok {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer tx.Rollback()
	actor, ok := s.actor(w, r, tx)
	if !ok {
		return
	}
	if !s.proofCurrent(r, tx, proof, sessionToken) {
		fail(w, 401, "authentication_required")
		return
	}
	p, _ := access.Current(r.Context())
	target, err := s.Member(r.Context(), tx, p.HouseholdID, r.PathValue("user"))
	if err != nil {
		fail(w, 404, "member_not_found")
		return
	}
	var assisted bool
	err = tx.QueryRowContext(r.Context(), "SELECT assisted=1 AND password_hash='!' FROM identity_users WHERE id=?", target.ID).Scan(&assisted)
	if err != nil || !assisted || !editable(actor, target) || !pinRole(target.Role) {
		fail(w, 403, "protected_member")
		return
	}
	if _, allowed := s.grant(actor, target.Role, normalizedPermissions(target.Permissions)); !allowed {
		fail(w, 403, "invalid_grant")
		return
	}
	s.issueAssistedEnrollment(w, r, tx, target.ID, actor, 200)
}

type enrollment struct{ User, Household, Name, Username, Issuer, IssuerMember string }

func (s *Service) enrollment(r *http.Request, tx *sql.Tx, token string) (enrollment, error) {
	var e enrollment
	digest, ok := tokenDigest(token)
	if !ok {
		return e, sql.ErrNoRows
	}
	err := tx.QueryRowContext(r.Context(), `SELECT e.user_id,e.household_id,u.name,u.username,e.issuer_id,e.issuer_member_id FROM identity_assisted_enrollments e JOIN identity_users u ON u.id=e.user_id JOIN identity_memberships m ON m.user_id=e.user_id AND m.household_id=e.household_id JOIN identity_member_ids i ON i.user_id=e.issuer_id AND i.household_id=e.household_id AND i.id=e.issuer_member_id WHERE e.token_hash=? AND e.expires_at>? AND u.assisted=1 AND u.password_hash='!' AND m.role IN ('member','restricted','guest') AND NOT EXISTS(SELECT 1 FROM identity_permissions p WHERE p.user_id=m.user_id AND p.household_id=m.household_id AND p.capability LIKE 'household.%')`, digest, time.Now().Unix()).Scan(&e.User, &e.Household, &e.Name, &e.Username, &e.Issuer, &e.IssuerMember)
	if err != nil {
		return e, err
	}
	actor, err := s.Member(r.Context(), tx, e.Household, e.Issuer)
	if err != nil || !manager(actor) || actor.MembershipID != e.IssuerMember {
		return e, sql.ErrNoRows
	}
	target, err := s.Member(r.Context(), tx, e.Household, e.User)
	if err != nil {
		return e, err
	}
	if _, ok := s.grant(actor, target.Role, normalizedPermissions(target.Permissions)); !ok {
		return e, sql.ErrNoRows
	}
	return e, nil
}
func (s *Service) inspectAssistedEnrollment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	digest, _ := tokenDigest(in.Token)
	if !s.limit(w, r, "enrollment-inspect:"+fmt.Sprintf("%x", digest)) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer tx.Rollback()
	e, err := s.enrollment(r, tx, in.Token)
	if err != nil {
		fail(w, 410, "enrollment_unavailable")
		return
	}
	modules.JSON(w, 200, map[string]string{"name": e.Name, "username": e.Username})
}
func (s *Service) completeAssistedEnrollment(w http.ResponseWriter, r *http.Request) {
	if _, ok := access.Current(r.Context()); ok {
		fail(w, 409, "sign_out_before_enrollment")
		return
	}
	var in struct {
		Token      string `json:"token"`
		PIN        string `json:"pin"`
		DeviceName string `json:"deviceName"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !pinPattern.MatchString(in.PIN) || !validText(strings.TrimSpace(in.DeviceName), 80) {
		fail(w, 400, "invalid_pin")
		return
	}
	digest, _ := tokenDigest(in.Token)
	if !s.limit(w, r, "enrollment-complete:"+fmt.Sprintf("%x", digest)) || !s.acquire(w) {
		return
	}
	defer func() { <-s.work }()
	// Reject invalid links before doing password derivation, then check again before commit.
	check, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	_, err = s.enrollment(r, check, in.Token)
	check.Rollback()
	if err != nil {
		fail(w, 410, "enrollment_unavailable")
		return
	}
	hash, err := hashPassword(in.PIN)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer tx.Rollback()
	e, err := s.enrollment(r, tx, in.Token)
	if err != nil {
		fail(w, 410, "enrollment_unavailable")
		return
	}
	// A replacement activates only this new device and signs out the lost/old ones.
	for _, q := range []string{"DELETE FROM identity_pin_devices WHERE user_id=?", "DELETE FROM identity_sessions WHERE user_id=?", "DELETE FROM identity_assisted_enrollments WHERE user_id=?"} {
		if _, err = tx.ExecContext(r.Context(), q, e.User); err != nil {
			fail(w, 503, "access_unavailable")
			return
		}
	}
	device, err := s.insertPINDevice(r, tx, e.User, e.Household, hash, in.DeviceName)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO identity_access_events(id,user_id,household_id,actor_id,action,created_at) VALUES(?,?,?,?,'device_activated',?)`, rand.Text(), e.User, e.Household, e.Issuer, time.Now().Unix()); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	// Do not silently switch an administrator's session to the assisted member.
	if err = tx.Commit(); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	s.setPINCookie(w, device)
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) insertPINDevice(r *http.Request, tx *sql.Tx, user, household, hash, name string) (string, error) {
	// Each browser holds a single device binding; replacing it also revokes the old binding.
	if c, err := r.Cookie(s.pinCookieName()); err == nil {
		digest := sha256.Sum256([]byte(c.Value))
		if _, err = tx.ExecContext(r.Context(), "DELETE FROM identity_pin_devices WHERE token_hash=?", digest[:]); err != nil {
			return "", err
		}
	}
	var count int
	if err := tx.QueryRowContext(r.Context(), "SELECT count(*) FROM identity_pin_devices WHERE user_id=?", user).Scan(&count); err != nil {
		return "", err
	}
	if count >= 10 {
		return "", errors.New("device limit")
	}
	token := randomCode()
	digest, _ := tokenDigest(token)
	now := time.Now()
	_, err := tx.ExecContext(r.Context(), `INSERT INTO identity_pin_devices(id,user_id,household_id,token_hash,pin_hash,name,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`, rand.Text(), user, household, digest, hash, strings.TrimSpace(name), now.Unix(), now.Add(pinDeviceLifetime).Unix())
	return token, err
}

type pinDevice struct{ ID, User, Household, Hash, Name, Username, PasswordHash string }

func (s *Service) device(r *http.Request, tx *sql.Tx, maximumFailures int) (pinDevice, error) {
	var d pinDevice
	c, err := r.Cookie(s.pinCookieName())
	if err != nil {
		return d, sql.ErrNoRows
	}
	digest, ok := tokenDigest(c.Value)
	if !ok {
		return d, sql.ErrNoRows
	}
	var q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	} = s.db
	if tx != nil {
		q = tx
	}
	err = q.QueryRowContext(r.Context(), `SELECT d.id,d.user_id,d.household_id,d.pin_hash,u.name,COALESCE(u.username,'u-'||lower(u.id)),u.password_hash FROM identity_pin_devices d JOIN identity_users u ON u.id=d.user_id JOIN identity_memberships m ON m.user_id=d.user_id AND m.household_id=d.household_id WHERE d.token_hash=? AND d.expires_at>? AND d.failures<? AND m.role IN ('member','restricted','guest') AND NOT EXISTS(SELECT 1 FROM identity_permissions p WHERE p.user_id=m.user_id AND p.household_id=m.household_id AND p.capability LIKE 'household.%')`, digest, time.Now().Unix(), maximumFailures).Scan(&d.ID, &d.User, &d.Household, &d.Hash, &d.Name, &d.Username, &d.PasswordHash)
	return d, err
}
func (s *Service) pinStatus(w http.ResponseWriter, r *http.Request) {
	d, err := s.device(r, nil, 5)
	if err == sql.ErrNoRows {
		modules.JSON(w, 200, map[string]bool{"available": false})
		return
	}
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	modules.JSON(w, 200, map[string]any{"available": true, "name": d.Name, "username": d.Username})
}
func (s *Service) pinLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PIN string `json:"pin"`
	}
	if !decode(w, r, &in) {
		return
	}
	cookie, _ := r.Cookie(s.pinCookieName())
	key := "unbound:" + r.RemoteAddr
	if cookie != nil {
		digest := sha256.Sum256([]byte(cookie.Value))
		key = fmt.Sprintf("%x", digest)
	}
	if !s.limit(w, r, "pin:"+key) || !s.acquire(w) {
		return
	}
	defer func() { <-s.work }()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	d, err := s.device(r, tx, 5)
	if err != nil {
		tx.Rollback()
		if err != sql.ErrNoRows {
			fail(w, 503, "access_unavailable")
			return
		}
		verifyPassword(in.PIN, s.dummyHash)
		fail(w, 401, "invalid_credentials")
		return
	}
	// Reserve one of five attempts before verification; failures persist across restarts and sessions.
	_, err = tx.ExecContext(r.Context(), "UPDATE identity_pin_devices SET failures=failures+1 WHERE id=?", d.ID)
	if err != nil {
		tx.Rollback()
		fail(w, 503, "access_unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	if !pinPattern.MatchString(in.PIN) || !verifyPassword(in.PIN, d.Hash) {
		fail(w, 401, "invalid_credentials")
		return
	}
	tx, err = s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer tx.Rollback()
	// Recheck binding, membership, role and password generation after concurrent recovery/removal.
	fresh, err := s.device(r, tx, 6)
	if err != nil || fresh.ID != d.ID || fresh.Hash != d.Hash || fresh.PasswordHash != d.PasswordHash {
		fail(w, 401, "invalid_credentials")
		return
	}
	token, err := s.createSession(r.Context(), tx, r, d.User, d.Household)
	if err == nil {
		digest := sha256.Sum256([]byte(token))
		_, err = tx.ExecContext(r.Context(), "INSERT INTO identity_pin_sessions(token_hash) VALUES(?)", digest[:])
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "UPDATE identity_pin_devices SET failures=0 WHERE id=?", d.ID)
	}
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	s.respondSession(w, r, token, 200)
}
func (s *Service) registerPINDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PIN        string `json:"pin"`
		Password   string `json:"password"`
		DeviceName string `json:"deviceName"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !pinPattern.MatchString(in.PIN) || !validText(strings.TrimSpace(in.DeviceName), 80) {
		fail(w, 400, "invalid_pin")
		return
	}
	proof, sessionToken, ok := s.passwordProof(w, r, in.Password)
	if !ok {
		return
	}
	hash, err := hashPassword(in.PIN)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer tx.Rollback()
	p, _ := access.Current(r.Context())
	m, err := s.Member(r.Context(), tx, p.HouseholdID, p.UserID)
	if err != nil || !pinRole(m.Role) || !s.proofCurrent(r, tx, proof, sessionToken) {
		fail(w, 403, "pin_not_allowed")
		return
	}
	for _, cap := range m.Permissions {
		if strings.HasPrefix(cap, "household.") {
			fail(w, 403, "pin_not_allowed")
			return
		}
	}
	token, err := s.insertPINDevice(r, tx, p.UserID, p.HouseholdID, hash, in.DeviceName)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	s.setPINCookie(w, token)
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) accessSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := access.Current(r.Context())
	if !ok {
		fail(w, 401, "authentication_required")
		return
	}
	current, _ := r.Context().Value(sessionKey{}).(*Session)
	if current == nil {
		fail(w, 401, "authentication_required")
		return
	}
	var username string
	var hasPassword bool
	if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(username,'u-'||lower(id)), password_hash!='!' FROM identity_users WHERE id=?", p.UserID).Scan(&username, &hasPassword); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	devices := []map[string]any{}
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,name,expires_at,failures>=5 FROM identity_pin_devices WHERE user_id=? AND household_id=? ORDER BY created_at DESC", p.UserID, p.HouseholdID)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var expiry int64
		var locked bool
		if err = rows.Scan(&id, &name, &expiry, &locked); err != nil {
			fail(w, 503, "access_unavailable")
			return
		}
		devices = append(devices, map[string]any{"id": id, "name": name, "expiresAt": time.Unix(expiry, 0).UTC(), "locked": locked})
	}
	if rows.Err() != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	allowed := pinRole(current.Role)
	for cap := range p.Permissions {
		if strings.HasPrefix(cap, "household.") {
			allowed = false
		}
	}
	modules.JSON(w, 200, map[string]any{"username": username, "hasPassword": hasPassword, "canUsePIN": allowed, "devices": devices})
}
func (s *Service) updateUsername(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	username, ok := normalizeUsername(in.Username)
	if !ok {
		fail(w, 400, "invalid_username")
		return
	}
	proof, sessionToken, ok := s.passwordProof(w, r, in.Password)
	if !ok {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer tx.Rollback()
	if !s.proofCurrent(r, tx, proof, sessionToken) {
		fail(w, 401, "authentication_required")
		return
	}
	p, _ := access.Current(r.Context())
	if _, err = tx.ExecContext(r.Context(), "UPDATE identity_users SET username=? WHERE id=?", username, p.UserID); err != nil {
		fail(w, 409, "account_identifier_taken")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) revokePINDevice(w http.ResponseWriter, r *http.Request) {
	p, ok := access.Current(r.Context())
	if !ok {
		fail(w, 401, "authentication_required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(r.Context(), "DELETE FROM identity_pin_devices WHERE id=? AND user_id=? AND household_id=?", r.PathValue("id"), p.UserID, p.HouseholdID)
	if err == nil {
		n, e := result.RowsAffected()
		if e != nil {
			err = e
		} else if n != 1 {
			fail(w, 404, "device_not_found")
			return
		}
	}
	if err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	// Revoking an unlock method also invalidates sessions it could have issued.
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM identity_sessions WHERE user_id=?", p.UserID); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "access_unavailable")
		return
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}
