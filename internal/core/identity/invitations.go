package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
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

func tokenDigest(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if len(token) != 43 || err != nil || len(raw) != 32 {
		return nil, false
	}
	digest := sha256.Sum256([]byte(token))
	return digest[:], true
}
func (s *Application) Invitations(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	if _, err := s.actor(ctx, nil); err != nil {
		return result, err
	}
	p, _ := access.Current(ctx)
	items, err := s.store.readInvitations(ctx, p.HouseholdID)
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	result.Data = items

	return result, nil
}
func (s *Application) Invite(ctx context.Context, client Client, input InviteInput) (Result, error) {
	result := Result{}

	email, ok := normalizeEmail(input.Email)
	if !ok {
		return result, fault.New(fault.Invalid, "invalid_email")
	}
	if _, err := s.actor(ctx, nil); err != nil {
		return result, err
	}
	if err := s.limit(ctx, client, "invite:"+email); err != nil {
		return result, err
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	defer tx.Rollback()
	actor, err := s.actor(ctx, tx)
	if err != nil {
		return result, err
	}
	p, _ := access.Current(ctx)
	permissions, ok := s.grant(actor, input.Role, input.Permissions)
	if !ok {
		return result, fault.New(fault.Denied, "invalid_grant")
	}
	var exists bool
	if err := s.store.isExistingHouseholdMember(ctx, tx, p.HouseholdID, email, &exists); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if exists {
		return result, fault.New(fault.Conflict, "already_member")
	}
	if actor.Role != "owner" {
		var protected bool
		if err := s.store.hasProtectedAdminInvitation(ctx, tx, p.HouseholdID, email, time.Now().Unix(), &protected); err != nil {
			return result, fault.New(fault.Unavailable, "invitations_unavailable")
		}
		if protected {
			return result, fault.New(fault.Denied, "permission_denied")
		}
	}
	now := time.Now().Unix()
	// Reissuing revokes the previous link for this email without storing either raw token.
	if _, err := s.store.revokePreviousInvitations(ctx, tx, now, p.HouseholdID, email); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	digest, _ := tokenDigest(token)
	id := rand.Text()
	encoded, _ := json.Marshal(permissions)
	_, err = s.store.insertInvitation(ctx, tx, id, p.HouseholdID, email, input.Role, string(encoded), digest, actor.ID, actor.MembershipID, now, now+int64(invitationLifetime.Seconds()))
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	url := s.origin.String() + "/join#" + token
	emailStatus := "manual"
	if input.SendEmail {
		emailStatus = "disabled"
		if s.mailer != nil {
			body := "Te han invitado a un hogar en HomeAtlas. Revisa y acepta la invitación aquí:\n\n" + url + "\n\nCaduca en 72 horas y solo puede usarse una vez. Si no esperabas esta invitación, ignora este mensaje.\n"
			emailStatus = "sent"
			if err := s.mailer.Send(ctx, email, "Invitación a un hogar en HomeAtlas", body); err != nil {
				emailStatus = "failed"
			}
		}
	}
	result.Data = map[string]any{"id": id, "url": url, "emailStatus": emailStatus, "expiresAt": time.Unix(now+int64(invitationLifetime.Seconds()), 0).UTC()}

	return result, nil
}
func (s *Application) RevokeInvitation(ctx context.Context, client Client, target string) (Result, error) {
	result := Result{}

	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	defer tx.Rollback()
	actor, err := s.actor(ctx, tx)
	if err != nil {
		return result, err
	}
	p, _ := access.Current(ctx)
	i, err := s.store.readInvitationForRevocation(ctx, tx, target, p.HouseholdID)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return result, fault.New(fault.Missing, "invitation_not_found")
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if actor.Role != "owner" && i.Role == "admin" {
		return result, fault.New(fault.Denied, "permission_denied")
	}
	if _, err := s.store.revokeInvitationRecord(ctx, tx, time.Now().Unix(), i.ID, p.HouseholdID); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}

	return result, nil
}

func (s *Application) validInvitation(ctx context.Context, tx unitofwork.Transaction, token string) (invitation, bool, error) {
	digest, ok := tokenDigest(token)
	if !ok {
		return invitation{}, false, nil
	}
	i, err := s.store.readInvitationByDigest(ctx, tx, digest)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return i, false, nil
	}
	if err != nil {
		return i, false, err
	}
	if i.Status != "pending" {
		return i, false, nil
	}
	issuer, err := s.Member(ctx, tx, i.household, i.issuer)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return i, false, nil
	}
	if err != nil {
		return i, false, err
	}
	_, allowed := s.grant(issuer, i.Role, normalizedPermissions(i.Permissions))
	return i, allowed && issuer.MembershipID == i.issuerMember, nil
}
func (s *Application) InspectInvitation(ctx context.Context, client Client, input InspectInvitationInput) (Result, error) {
	result := Result{}

	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	defer tx.Rollback()
	i, ok, err := s.validInvitation(ctx, tx, input.Token)
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if !ok {
		return result, fault.New(fault.Expired, "invitation_unavailable")
	}
	var exists bool
	if err := s.store.invitationAccountExists(ctx, tx, i.Email, &exists); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	result.Data = map[string]any{"email": i.Email, "householdName": i.householdName, "role": i.Role, "permissions": i.Permissions, "expiresAt": i.ExpiresAt, "needsAccount": !exists}

	return result, nil
}
func (s *Application) AcceptInvitation(ctx context.Context, client Client, input AcceptInvitationInput) (Result, error) {
	result := Result{}

	if err := s.acquire(); err != nil {
		return result, err
	}
	defer func() { <-s.work }()
	// Validate before password work, then recheck under the acceptance transaction.
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	i, ok, err := s.validInvitation(ctx, tx, input.Token)
	tx.Rollback()
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if !ok {
		return result, fault.New(fault.Expired, "invitation_unavailable")
	}
	if err := s.limit(ctx, client, i.Email); err != nil {
		return result, err
	}
	var userID, stored string
	err = s.store.readInvitedAccount(ctx, nil, i.Email, &userID, &stored)
	newUser := errors.Is(err, unitofwork.ErrNotFound)
	if err != nil && !newUser {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	current, _ := ctx.Value(sessionKey{}).(*Session)
	if current != nil && current.User.Email != i.Email {
		return result, fault.New(fault.Denied, "invitation_account_mismatch")
	}
	if newUser {
		input.Name = strings.TrimSpace(input.Name)
		if !validText(input.Name, 80) || !validNewPassword(input.Password) {
			return result, fault.New(fault.Invalid, "invalid_account")
		}
		stored, err = hashPassword(input.Password)
		if err != nil {
			return result, fault.New(fault.Unavailable, "invitations_unavailable")
		}
		userID = rand.Text()
	} else if current == nil {
		if len(input.Password) == 0 || len(input.Password) > 512 || !verifyPassword(input.Password, stored) {
			return result, fault.New(fault.Unauthenticated, "invalid_credentials")
		}
	}
	tx, err = s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	defer tx.Rollback()
	fresh, ok, err := s.validInvitation(ctx, tx, input.Token)
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if !ok {
		return result, fault.New(fault.Expired, "invitation_unavailable")
	}
	// Account state may have changed during the password derivation; never reset an existing account.
	if !newUser {
		var unchanged bool
		if err := s.store.invitedAccountUnchanged(ctx, tx, userID, stored, &unchanged); err != nil {
			return result, fault.New(fault.Unavailable, "invitations_unavailable")
		}
		if current != nil {
			if !client.HasSession {
				return result, fault.New(fault.Unauthenticated, "invalid_credentials")
			}
			digest := sha256.Sum256([]byte(client.SessionToken))
			var active bool
			if err := s.store.isInvitedSessionActive(ctx, tx, digest[:], userID, time.Now().Unix(), time.Now().Unix()-int64(sessionIdle.Seconds()), &active); err != nil {
				return result, fault.New(fault.Unavailable, "invitations_unavailable")
			}
			unchanged = unchanged && active
		}
		if !unchanged {
			return result, fault.New(fault.Unauthenticated, "invalid_credentials")
		}
	}
	if newUser {
		var exists bool
		if err := s.store.isInvitedMailboxTaken(ctx, tx, fresh.Email, &exists); err != nil {
			return result, fault.New(fault.Unavailable, "invitations_unavailable")
		}
		if exists {
			return result, fault.New(fault.Conflict, "account_changed")
		}
		if _, err := s.store.insertInvitedAccount(ctx, tx, userID, input.Name, fresh.Email, stored); err != nil {
			return result, fault.New(fault.Unavailable, "invitations_unavailable")
		}
	}
	var exists bool
	if err := s.store.hasInvitedMembership(ctx, tx, fresh.household, userID, &exists); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if exists {
		return result, fault.New(fault.Conflict, "already_member")
	}
	if _, err := s.store.insertInvitedMembership(ctx, tx, userID, fresh.household, fresh.Role); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if err := s.setPermissions(ctx, tx, fresh.household, userID, fresh.Permissions); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	mutation, err := s.store.claimInvitation(ctx, tx, time.Now().Unix(), fresh.ID, time.Now().Unix())
	if err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	if count, err := mutation.Affected, mutation.CountError; err != nil || count != 1 {
		return result, fault.New(fault.Expired, "invitation_unavailable")
	}
	token, err := s.createSession(ctx, tx, client, userID, fresh.household)
	if err != nil {
		return result, fault.New(fault.Unavailable, "session_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "invitations_unavailable")
	}
	result.SessionToken = token

	return result, nil
}
