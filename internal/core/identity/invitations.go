package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

const invitationLifetime = 72 * time.Hour

type invitation struct {
	ID                                             string    `json:"id"`
	Email                                          string    `json:"email"`
	Role                                           string    `json:"role"`
	Permissions                                    []string  `json:"permissions"`
	ExpiresAt                                      time.Time `json:"expiresAt"`
	Status                                         string    `json:"status"`
	household, issuer, issuerMember, householdName string
	expires, accepted, revoked                     int64
}

const invitationColumns = `i.id,i.email,i.role,i.permissions,i.household_id,i.issuer_id,i.issuer_member_id,i.expires_at,i.accepted_at,i.revoked_at,h.name`

func readInvitation(row interface{ Scan(...any) error }) (invitation, error) {
	var i invitation
	var permissions string
	err := row.Scan(&i.ID, &i.Email, &i.Role, &permissions, &i.household, &i.issuer, &i.issuerMember, &i.expires, &i.accepted, &i.revoked, &i.householdName)
	if err == nil {
		err = json.Unmarshal([]byte(permissions), &i.Permissions)
	}
	i.ExpiresAt = time.Unix(i.expires, 0).UTC()
	i.Status = "pending"
	if i.expires <= time.Now().Unix() {
		i.Status = "expired"
	}
	if i.revoked != 0 {
		i.Status = "revoked"
	}
	if i.accepted != 0 {
		i.Status = "accepted"
	}
	return i, err
}
func tokenDigest(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if len(token) != 43 || err != nil || len(raw) != 32 {
		return nil, false
	}
	digest := sha256.Sum256([]byte(token))
	return digest[:], true
}
func (s *Service) invitations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.actor(w, r, nil); !ok {
		return
	}
	p, _ := access.Current(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT `+invitationColumns+` FROM identity_invitations i JOIN identity_households h ON h.id=i.household_id WHERE i.household_id=? ORDER BY i.created_at DESC,i.id LIMIT 100`, p.HouseholdID)
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	defer rows.Close()
	items := []invitation{}
	for rows.Next() {
		i, err := readInvitation(rows)
		if err != nil {
			fail(w, 503, "invitations_unavailable")
			return
		}
		items = append(items, i)
	}
	if rows.Err() != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	modules.JSON(w, 200, items)
}
func (s *Service) invite(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string   `json:"email"`
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
		SendEmail   bool     `json:"sendEmail"`
	}
	if !decode(w, r, &input) {
		return
	}
	email, ok := normalizeEmail(input.Email)
	if !ok {
		fail(w, 400, "invalid_email")
		return
	}
	if _, ok := s.actor(w, r, nil); !ok {
		return
	}
	if !s.limit(w, r, "invite:"+email) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	defer tx.Rollback()
	actor, ok := s.actor(w, r, tx)
	if !ok {
		return
	}
	p, _ := access.Current(r.Context())
	permissions, ok := s.grant(actor, input.Role, input.Permissions)
	if !ok {
		fail(w, 403, "invalid_grant")
		return
	}
	var exists bool
	if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_memberships m JOIN identity_users u ON u.id=m.user_id WHERE m.household_id=? AND u.email=?)`, p.HouseholdID, email).Scan(&exists); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if exists {
		fail(w, 409, "already_member")
		return
	}
	if actor.Role != "owner" {
		var protected bool
		if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_invitations WHERE household_id=? AND email=? AND role='admin' AND accepted_at=0 AND revoked_at=0 AND expires_at>?)`, p.HouseholdID, email, time.Now().Unix()).Scan(&protected); err != nil {
			fail(w, 503, "invitations_unavailable")
			return
		}
		if protected {
			fail(w, 403, "permission_denied")
			return
		}
	}
	now := time.Now().Unix()
	// Reissuing revokes the previous link for this email without storing either raw token.
	if _, err := tx.ExecContext(r.Context(), `UPDATE identity_invitations SET revoked_at=? WHERE household_id=? AND email=? AND accepted_at=0 AND revoked_at=0`, now, p.HouseholdID, email); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	digest, _ := tokenDigest(token)
	id := rand.Text()
	encoded, _ := json.Marshal(permissions)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO identity_invitations(id,household_id,email,role,permissions,token_hash,issuer_id,issuer_member_id,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, p.HouseholdID, email, input.Role, string(encoded), digest, actor.ID, actor.MembershipID, now, now+int64(invitationLifetime.Seconds()))
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	url := s.origin.String() + "/join#" + token
	emailStatus := "manual"
	if input.SendEmail {
		emailStatus = "disabled"
		if s.mailer != nil {
			body := "Te han invitado a un hogar en HomeAtlas. Revisa y acepta la invitación aquí:\n\n" + url + "\n\nCaduca en 72 horas y solo puede usarse una vez. Si no esperabas esta invitación, ignora este mensaje.\n"
			emailStatus = "sent"
			if err := s.mailer.Send(r.Context(), email, "Invitación a un hogar en HomeAtlas", body); err != nil {
				emailStatus = "failed"
			}
		}
	}
	modules.JSON(w, 201, map[string]any{"id": id, "url": url, "emailStatus": emailStatus, "expiresAt": time.Unix(now+int64(invitationLifetime.Seconds()), 0).UTC()})
}
func (s *Service) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	defer tx.Rollback()
	actor, ok := s.actor(w, r, tx)
	if !ok {
		return
	}
	p, _ := access.Current(r.Context())
	i, err := readInvitation(tx.QueryRowContext(r.Context(), `SELECT `+invitationColumns+` FROM identity_invitations i JOIN identity_households h ON h.id=i.household_id WHERE i.id=? AND i.household_id=?`, r.PathValue("id"), p.HouseholdID))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "invitation_not_found")
		return
	}
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if actor.Role != "owner" && i.Role == "admin" {
		fail(w, 403, "permission_denied")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE identity_invitations SET revoked_at=? WHERE id=? AND household_id=? AND accepted_at=0`, time.Now().Unix(), i.ID, p.HouseholdID); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

func (s *Service) validInvitation(r *http.Request, tx *sql.Tx, token string) (invitation, bool, error) {
	digest, ok := tokenDigest(token)
	if !ok {
		return invitation{}, false, nil
	}
	i, err := readInvitation(tx.QueryRowContext(r.Context(), `SELECT `+invitationColumns+` FROM identity_invitations i JOIN identity_households h ON h.id=i.household_id WHERE i.token_hash=?`, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return i, false, nil
	}
	if err != nil {
		return i, false, err
	}
	if i.Status != "pending" {
		return i, false, nil
	}
	issuer, err := s.Member(r.Context(), tx, i.household, i.issuer)
	if errors.Is(err, sql.ErrNoRows) {
		return i, false, nil
	}
	if err != nil {
		return i, false, err
	}
	_, allowed := s.grant(issuer, i.Role, normalizedPermissions(i.Permissions))
	return i, allowed && issuer.MembershipID == i.issuerMember, nil
}
func (s *Service) inspectInvitation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &input) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	defer tx.Rollback()
	i, ok, err := s.validInvitation(r, tx, input.Token)
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if !ok {
		fail(w, 410, "invitation_unavailable")
		return
	}
	var exists bool
	if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_users WHERE email=?)`, i.Email).Scan(&exists); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	modules.JSON(w, 200, map[string]any{"email": i.Email, "householdName": i.householdName, "role": i.Role, "permissions": i.Permissions, "expiresAt": i.ExpiresAt, "needsAccount": !exists})
}
func (s *Service) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !s.acquire(w) {
		return
	}
	defer func() { <-s.work }()
	// Validate before password work, then recheck under the acceptance transaction.
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	i, ok, err := s.validInvitation(r, tx, input.Token)
	tx.Rollback()
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if !ok {
		fail(w, 410, "invitation_unavailable")
		return
	}
	if !s.limit(w, r, i.Email) {
		return
	}
	var userID, stored string
	err = s.db.QueryRowContext(r.Context(), `SELECT id,password_hash FROM identity_users WHERE email=?`, i.Email).Scan(&userID, &stored)
	newUser := errors.Is(err, sql.ErrNoRows)
	if err != nil && !newUser {
		fail(w, 503, "invitations_unavailable")
		return
	}
	current, _ := r.Context().Value(sessionKey{}).(*Session)
	if current != nil && current.User.Email != i.Email {
		fail(w, 403, "invitation_account_mismatch")
		return
	}
	if newUser {
		input.Name = strings.TrimSpace(input.Name)
		if !validText(input.Name, 80) || !validNewPassword(input.Password) {
			fail(w, 400, "invalid_account")
			return
		}
		stored, err = hashPassword(input.Password)
		if err != nil {
			fail(w, 503, "invitations_unavailable")
			return
		}
		userID = rand.Text()
	} else if current == nil {
		if len(input.Password) == 0 || len(input.Password) > 512 || !verifyPassword(input.Password, stored) {
			fail(w, 401, "invalid_credentials")
			return
		}
	}
	tx, err = s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	defer tx.Rollback()
	fresh, ok, err := s.validInvitation(r, tx, input.Token)
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if !ok {
		fail(w, 410, "invitation_unavailable")
		return
	}
	// Account state may have changed during the password derivation; never reset an existing account.
	if !newUser {
		var unchanged bool
		if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_users WHERE id=? AND password_hash=?)`, userID, stored).Scan(&unchanged); err != nil {
			fail(w, 503, "invitations_unavailable")
			return
		}
		if current != nil {
			cookie, err := r.Cookie(s.cookieName)
			if err != nil {
				fail(w, 401, "invalid_credentials")
				return
			}
			digest := sha256.Sum256([]byte(cookie.Value))
			var active bool
			if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_sessions WHERE token_hash=? AND user_id=? AND expires_at>? AND last_seen>?)`, digest[:], userID, time.Now().Unix(), time.Now().Unix()-int64(sessionIdle.Seconds())).Scan(&active); err != nil {
				fail(w, 503, "invitations_unavailable")
				return
			}
			unchanged = unchanged && active
		}
		if !unchanged {
			fail(w, 401, "invalid_credentials")
			return
		}
	}
	if newUser {
		var exists bool
		if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_users WHERE email=?)`, fresh.Email).Scan(&exists); err != nil {
			fail(w, 503, "invitations_unavailable")
			return
		}
		if exists {
			fail(w, 409, "account_changed")
			return
		}
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO identity_users(id,name,email,password_hash) VALUES(?,?,?,?)`, userID, input.Name, fresh.Email, stored); err != nil {
			fail(w, 503, "invitations_unavailable")
			return
		}
	}
	var exists bool
	if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_memberships WHERE household_id=? AND user_id=?)`, fresh.household, userID).Scan(&exists); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if exists {
		fail(w, 409, "already_member")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO identity_memberships VALUES(?,?,?)`, userID, fresh.household, fresh.Role); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if err := setPermissions(r.Context(), tx, fresh.household, userID, fresh.Permissions); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	result, err := tx.ExecContext(r.Context(), `UPDATE identity_invitations SET accepted_at=? WHERE id=? AND accepted_at=0 AND revoked_at=0 AND expires_at>?`, time.Now().Unix(), fresh.ID, time.Now().Unix())
	if err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		fail(w, 410, "invitation_unavailable")
		return
	}
	token, err := s.createSession(r.Context(), tx, r, userID, fresh.household)
	if err != nil {
		fail(w, 503, "session_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "invitations_unavailable")
		return
	}
	s.respondSession(w, r, token, 201)
}
