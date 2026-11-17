package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
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
func nullableEmail(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// Fresh password proof is rechecked in the committing transaction, including session validity.
func (s *Application) passwordProof(ctx context.Context, client Client, password string) (string, string, error) {
	p, ok := access.Current(ctx)
	if !ok {
		return "", "", fault.New(fault.Unauthenticated, "authentication_required")
	}
	if err := s.limit(ctx, client, "access:"+p.UserID); err != nil {
		return "", "", err
	}
	if err := s.acquire(); err != nil {
		return "", "", err
	}
	defer func() { <-s.work }()
	var stored string
	if err := s.store.readPasswordProof(ctx, nil, p.UserID, &stored); err != nil {
		return "", "", fault.New(fault.Unavailable, "access_unavailable")
	}
	if len(password) > 512 || !verifyPassword(password, stored) {
		return "", "", fault.New(fault.Denied, "password_confirmation_required")
	}
	if !client.HasSession {
		return "", "", fault.New(fault.Unauthenticated, "authentication_required")
	}
	return stored, client.SessionToken, nil
}
func (s *Application) proofCurrent(ctx context.Context, tx unitofwork.Transaction, stored, token string) bool {
	p, ok := access.Current(ctx)
	if !ok {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	now := time.Now().Unix()
	var valid bool
	err := s.store.isPasswordProofCurrent(ctx, tx, digest[:], p.UserID, p.HouseholdID, now, now-int64(sessionIdle.Seconds()), stored, p.MembershipID, &valid)
	return err == nil && valid
}
func pinRole(role string) bool { return role == "member" || role == "restricted" || role == "guest" }

type AssistedInput struct {
	Name        string   `json:"name"`
	Username    string   `json:"username"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	Password    string   `json:"password"`
}

func (s *Application) CreateAssistedMember(ctx context.Context, client Client, in AssistedInput) (Result, error) {
	result := Result{}

	in.Name = strings.TrimSpace(in.Name)
	username, valid := normalizeUsername(in.Username)
	email, emailOK := optionalEmail(in.Email)
	if !valid || !emailOK || !validText(in.Name, 80) || !pinRole(in.Role) {
		return result, fault.New(fault.Invalid, "invalid_assisted_member")
	}
	proof, sessionToken, err := s.passwordProof(ctx, client, in.Password)
	if err != nil {
		return result, err
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	defer tx.Rollback()
	actor, err := s.actor(ctx, tx)
	if err != nil {
		return result, err
	}
	if !s.proofCurrent(ctx, tx, proof, sessionToken) {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	permissions, ok := s.grant(actor, in.Role, in.Permissions)
	if !ok {
		return result, fault.New(fault.Denied, "invalid_grant")
	}
	// PIN accounts never receive installation or household administration capabilities.
	for _, p := range permissions {
		if strings.HasPrefix(p, "household.") {
			return result, fault.New(fault.Denied, "invalid_grant")
		}
	}
	var exists bool
	err = s.store.assistedAccountExists(ctx, tx, username, nullableEmail(email), &exists)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if exists {
		return result, fault.New(fault.Conflict, "account_identifier_taken")
	}
	user := rand.Text()
	p, _ := access.Current(ctx)
	if _, err = s.store.insertAssistedAccount(ctx, tx, user, in.Name, nullableEmail(email), username); err != nil {
		return result, fault.New(fault.Conflict, "account_identifier_taken")
	}
	if _, err = s.store.insertAssistedMembership(ctx, tx, user, p.HouseholdID, in.Role); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if err = s.setPermissions(ctx, tx, p.HouseholdID, user, permissions); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	return s.issueAssistedEnrollment(ctx, client, tx, user, actor)
}
func (s *Application) issueAssistedEnrollment(ctx context.Context, client Client, tx unitofwork.Transaction, user string, actor access.Member) (Result, error) {
	result := Result{}

	p, _ := access.Current(ctx)
	token := randomCode()
	digest, _ := tokenDigest(token)
	expires := time.Now().Add(recoveryLifetime)
	_, err := s.store.saveAssistedEnrollment(ctx, tx, user, p.HouseholdID, digest, actor.ID, actor.MembershipID, expires.Unix())
	if err == nil {
		_, err = s.store.recordEnrollmentIssued(ctx, tx, rand.Text(), user, p.HouseholdID, actor.ID, time.Now().Unix())
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if err = tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	result.Data = map[string]any{"url": s.origin.String() + "/activate-access#" + token, "expiresAt": expires, "userId": user}

	return result, nil
}
func (s *Application) RenewAssistedEnrollment(ctx context.Context, client Client, targetUser string, in RenewAssistedEnrollmentInput) (Result, error) {
	result := Result{}

	proof, sessionToken, err := s.passwordProof(ctx, client, in.Password)
	if err != nil {
		return result, err
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	defer tx.Rollback()
	actor, err := s.actor(ctx, tx)
	if err != nil {
		return result, err
	}
	if !s.proofCurrent(ctx, tx, proof, sessionToken) {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	p, _ := access.Current(ctx)
	target, err := s.Member(ctx, tx, p.HouseholdID, targetUser)
	if err != nil {
		return result, fault.New(fault.Missing, "member_not_found")
	}
	var assisted bool
	err = s.store.isAssistedAccount(ctx, tx, target.ID, &assisted)
	if err != nil || !assisted || !editable(actor, target) || !pinRole(target.Role) {
		return result, fault.New(fault.Denied, "protected_member")
	}
	if _, allowed := s.grant(actor, target.Role, normalizedPermissions(target.Permissions)); !allowed {
		return result, fault.New(fault.Denied, "invalid_grant")
	}
	return s.issueAssistedEnrollment(ctx, client, tx, target.ID, actor)
}

type enrollment struct{ User, Household, Name, Username, Issuer, IssuerMember string }

func (s *Application) enrollment(ctx context.Context, tx unitofwork.Transaction, token string) (enrollment, error) {
	var e enrollment
	digest, ok := tokenDigest(token)
	if !ok {
		return e, unitofwork.ErrNotFound
	}
	err := s.store.readEnrollment(ctx, tx, digest, time.Now().Unix(), &e.User, &e.Household, &e.Name, &e.Username, &e.Issuer, &e.IssuerMember)
	if err != nil {
		return e, err
	}
	actor, err := s.Member(ctx, tx, e.Household, e.Issuer)
	if err != nil || !manager(actor) || actor.MembershipID != e.IssuerMember {
		return e, unitofwork.ErrNotFound
	}
	target, err := s.Member(ctx, tx, e.Household, e.User)
	if err != nil {
		return e, err
	}
	if _, ok := s.grant(actor, target.Role, normalizedPermissions(target.Permissions)); !ok {
		return e, unitofwork.ErrNotFound
	}
	return e, nil
}
func (s *Application) InspectAssistedEnrollment(ctx context.Context, client Client, in InspectAssistedEnrollmentInput) (Result, error) {
	result := Result{}

	digest, _ := tokenDigest(in.Token)
	if err := s.limit(ctx, client, "enrollment-inspect:"+fmt.Sprintf("%x", digest)); err != nil {
		return result, err
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	defer tx.Rollback()
	e, err := s.enrollment(ctx, tx, in.Token)
	if err != nil {
		return result, fault.New(fault.Expired, "enrollment_unavailable")
	}
	result.Data = map[string]string{"name": e.Name, "username": e.Username}

	return result, nil
}
func (s *Application) CompleteAssistedEnrollment(ctx context.Context, client Client, in CompleteAssistedEnrollmentInput) (Result, error) {
	result := Result{}

	if _, ok := access.Current(ctx); ok {
		return result, fault.New(fault.Conflict, "sign_out_before_enrollment")
	}

	if !pinPattern.MatchString(in.PIN) || !validText(strings.TrimSpace(in.DeviceName), 80) {
		return result, fault.New(fault.Invalid, "invalid_pin")
	}
	digest, _ := tokenDigest(in.Token)
	if err := s.limit(ctx, client, "enrollment-complete:"+fmt.Sprintf("%x", digest)); err != nil {
		return result, err
	}
	if err := s.acquire(); err != nil {
		return result, err
	}
	defer func() { <-s.work }()
	// Reject invalid links before doing password derivation, then check again before commit.
	check, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	_, err = s.enrollment(ctx, check, in.Token)
	check.Rollback()
	if err != nil {
		return result, fault.New(fault.Expired, "enrollment_unavailable")
	}
	hash, err := hashPassword(in.PIN)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	defer tx.Rollback()
	e, err := s.enrollment(ctx, tx, in.Token)
	if err != nil {
		return result, fault.New(fault.Expired, "enrollment_unavailable")
	}
	// A replacement activates only this new device and signs out the lost/old ones.
	if err := s.store.revokeAssistedCredentials(ctx, tx, e.User); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	device, err := s.insertPINDevice(ctx, client, tx, e.User, e.Household, hash, in.DeviceName)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if _, err = s.store.recordDeviceActivated(ctx, tx, rand.Text(), e.User, e.Household, e.Issuer, time.Now().Unix()); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	// Do not silently switch an administrator's session to the assisted member.
	if err = tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	result.DeviceToken = device

	return result, nil
}
func (s *Application) insertPINDevice(ctx context.Context, client Client, tx unitofwork.Transaction, user, household, hash, name string) (string, error) {
	// Each browser holds a single device binding; replacing it also revokes the old binding.
	if client.HasDevice {
		digest := sha256.Sum256([]byte(client.DeviceToken))
		if _, err := s.store.deletePreviousPINBinding(ctx, tx, digest[:]); err != nil {
			return "", err
		}
	}
	var count int
	if err := s.store.countPINDevices(ctx, tx, user, &count); err != nil {
		return "", err
	}
	if count >= 10 {
		return "", errors.New("device limit")
	}
	token := randomCode()
	digest, _ := tokenDigest(token)
	now := time.Now()
	_, err := s.store.insertDeviceBinding(ctx, tx, rand.Text(), user, household, digest, hash, strings.TrimSpace(name), now.Unix(), now.Add(pinDeviceLifetime).Unix())
	return token, err
}

type pinDevice struct{ ID, User, Household, Hash, Name, Username, PasswordHash string }

func (s *Application) device(ctx context.Context, client Client, tx unitofwork.Transaction, maximumFailures int) (pinDevice, error) {
	var d pinDevice
	if !client.HasDevice {
		return d, unitofwork.ErrNotFound
	}
	digest, ok := tokenDigest(client.DeviceToken)
	if !ok {
		return d, unitofwork.ErrNotFound
	}
	err := s.store.readDeviceBinding(ctx, tx, digest, time.Now().Unix(), maximumFailures, &d.ID, &d.User, &d.Household, &d.Hash, &d.Name, &d.Username, &d.PasswordHash)
	return d, err
}
func (s *Application) PinStatus(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	d, err := s.device(ctx, client, nil, 5)
	if errors.Is(err, unitofwork.ErrNotFound) {
		result.Data = map[string]bool{"available": false}
		return result, nil
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	result.Data = map[string]any{"available": true, "name": d.Name, "username": d.Username}

	return result, nil
}
func (s *Application) PinLogin(ctx context.Context, client Client, in PINLoginInput) (Result, error) {
	result := Result{}

	key := "unbound:" + client.RemoteAddress
	if client.HasDevice {
		digest := sha256.Sum256([]byte(client.DeviceToken))
		key = fmt.Sprintf("%x", digest)
	}
	if err := s.limit(ctx, client, "pin:"+key); err != nil {
		return result, err
	}
	if err := s.acquire(); err != nil {
		return result, err
	}
	defer func() { <-s.work }()
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	d, err := s.device(ctx, client, tx, 5)
	if err != nil {
		tx.Rollback()
		if !errors.Is(err, unitofwork.ErrNotFound) {
			return result, fault.New(fault.Unavailable, "access_unavailable")
		}
		verifyPassword(in.PIN, s.dummyHash)
		return result, fault.New(fault.Unauthenticated, "invalid_credentials")
	}
	// Reserve one of five attempts before verification; failures persist across restarts and sessions.
	_, err = s.store.recordPINFailure(ctx, tx, d.ID)
	if err != nil {
		tx.Rollback()
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if err = tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if !pinPattern.MatchString(in.PIN) || !verifyPassword(in.PIN, d.Hash) {
		return result, fault.New(fault.Unauthenticated, "invalid_credentials")
	}
	tx, err = s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	defer tx.Rollback()
	// Recheck binding, membership, role and password generation after concurrent recovery/removal.
	fresh, err := s.device(ctx, client, tx, 6)
	if err != nil || fresh.ID != d.ID || fresh.Hash != d.Hash || fresh.PasswordHash != d.PasswordHash {
		return result, fault.New(fault.Unauthenticated, "invalid_credentials")
	}
	token, err := s.createSession(ctx, tx, client, d.User, d.Household)
	if err == nil {
		digest := sha256.Sum256([]byte(token))
		_, err = s.store.markPINSession(ctx, tx, digest[:])
	}
	if err == nil {
		_, err = s.store.resetPINFailures(ctx, tx, d.ID)
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if err = tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	result.SessionToken = token

	return result, nil
}
func (s *Application) RegisterPINDevice(ctx context.Context, client Client, in RegisterPINDeviceInput) (Result, error) {
	result := Result{}

	if !pinPattern.MatchString(in.PIN) || !validText(strings.TrimSpace(in.DeviceName), 80) {
		return result, fault.New(fault.Invalid, "invalid_pin")
	}
	proof, sessionToken, err := s.passwordProof(ctx, client, in.Password)
	if err != nil {
		return result, err
	}
	hash, err := hashPassword(in.PIN)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	defer tx.Rollback()
	p, _ := access.Current(ctx)
	m, err := s.Member(ctx, tx, p.HouseholdID, p.UserID)
	if err != nil || !pinRole(m.Role) || !s.proofCurrent(ctx, tx, proof, sessionToken) {
		return result, fault.New(fault.Denied, "pin_not_allowed")
	}
	for _, cap := range m.Permissions {
		if strings.HasPrefix(cap, "household.") {
			return result, fault.New(fault.Denied, "pin_not_allowed")
		}
	}
	token, err := s.insertPINDevice(ctx, client, tx, p.UserID, p.HouseholdID, hash, in.DeviceName)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if err = tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	result.DeviceToken = token

	return result, nil
}
func (s *Application) AccessSettings(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	p, ok := access.Current(ctx)
	if !ok {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	current, _ := ctx.Value(sessionKey{}).(*Session)
	if current == nil {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	var username string
	var hasPassword bool
	if err := s.store.readAccountAccess(ctx, nil, p.UserID, &username, &hasPassword); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	devices, err := s.store.readPINDevices(ctx, p.UserID, p.HouseholdID)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	allowed := pinRole(current.Role)
	for cap := range p.Permissions {
		if strings.HasPrefix(cap, "household.") {
			allowed = false
		}
	}
	result.Data = map[string]any{"username": username, "hasPassword": hasPassword, "canUsePIN": allowed, "devices": devices}

	return result, nil
}
func (s *Application) UpdateUsername(ctx context.Context, client Client, in UpdateUsernameInput) (Result, error) {
	result := Result{}

	username, ok := normalizeUsername(in.Username)
	if !ok {
		return result, fault.New(fault.Invalid, "invalid_username")
	}
	proof, sessionToken, err := s.passwordProof(ctx, client, in.Password)
	if err != nil {
		return result, err
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	defer tx.Rollback()
	if !s.proofCurrent(ctx, tx, proof, sessionToken) {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	p, _ := access.Current(ctx)
	if _, err = s.store.saveUsername(ctx, tx, username, p.UserID); err != nil {
		return result, fault.New(fault.Conflict, "account_identifier_taken")
	}
	if err = tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}

	return result, nil
}
func (s *Application) RevokePINDevice(ctx context.Context, client Client, target string) (Result, error) {
	result := Result{}

	p, ok := access.Current(ctx)
	if !ok {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	defer tx.Rollback()
	mutation, err := s.store.deleteDeviceBinding(ctx, tx, target, p.UserID, p.HouseholdID)
	if err == nil {
		n, e := mutation.Affected, mutation.CountError
		if e != nil {
			err = e
		} else if n != 1 {
			return result, fault.New(fault.Missing, "device_not_found")
		}
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	// Revoking an unlock method also invalidates sessions it could have issued.
	if _, err = s.store.revokeDeviceSessions(ctx, tx, p.UserID); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	if err = tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "access_unavailable")
	}
	result.EndSession = true

	return result, nil
}
