package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

func (s *Application) initialized(ctx context.Context) (bool, error) {
	var initialized bool
	err := s.store.readInitialized(ctx, nil, &initialized)
	return initialized, err
}

func (s *Application) Session(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	ready, err := s.initialized(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "session_unavailable")
	}
	current, _ := ctx.Value(sessionKey{}).(*Session)
	result.Data = State{SetupRequired: !ready, Session: current, SMTPEnabled: s.mailer != nil}

	return result, nil
}

type SetupInput struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	HouseholdName string `json:"householdName"`
	Timezone      string `json:"timezone"`
}

func (s *Application) Setup(ctx context.Context, client Client, input SetupInput) (Result, error) {
	result := Result{}

	ready, err := s.initialized(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "setup_unavailable")
	}
	if ready {
		return result, fault.New(fault.Conflict, "setup_completed")
	}

	if err := s.limit(ctx, client, "setup"); err != nil {
		return result, err
	}
	if !s.validSetupCode(input.Code) {
		return result, fault.New(fault.Denied, "invalid_setup_code")
	}
	input.Name = strings.TrimSpace(input.Name)
	input.HouseholdName = strings.TrimSpace(input.HouseholdName)
	email, ok := optionalEmail(input.Email)
	username, userOK := normalizeUsername(input.Username)
	if input.Username == "" && email != "" {
		username, userOK = "u-"+strings.ToLower(rand.Text()), true
	}
	if !validTimezone(input.Timezone) {
		return result, fault.New(fault.Invalid, "invalid_setup")
	}
	if !ok || !userOK || !validText(input.Name, 80) || !validText(input.HouseholdName, 100) || !validNewPassword(input.Password) {
		return result, fault.New(fault.Invalid, "invalid_setup")
	}
	if err := s.acquire(); err != nil {
		return result, err
	}
	defer func() { <-s.work }()
	if err := s.limit(ctx, client, email); err != nil {
		return result, err
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	defer tx.Rollback()
	mutation, err := s.store.claimInstallation(ctx, tx)
	if err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	if count, err := mutation.Affected, mutation.CountError; err != nil || count != 1 {
		return result, fault.New(fault.Conflict, "setup_completed")
	}
	userID, householdID := rand.Text(), rand.Text()
	if input.Username == "" {
		username = "u-" + strings.ToLower(userID)
	}
	if _, err := s.store.insertOwnerAccount(ctx, tx, userID, input.Name, nullableEmail(email), hash, username); err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	if _, err := s.store.insertOwnerHousehold(ctx, tx, householdID, input.HouseholdName, input.Timezone); err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	if _, err := s.store.insertOwnerMembership(ctx, tx, userID, householdID); err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	for _, capability := range s.ownerCapabilities {
		if _, err := s.store.insertOwnerCapability(ctx, tx, userID, householdID, capability); err != nil {
			return result, fault.New(fault.Internal, "setup_unavailable")
		}
	}
	token, err := s.createSession(ctx, tx, client, userID, householdID)
	if err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	s.removeSetupCode()
	result.SessionToken = token

	return result, nil
}

type LoginInput struct {
	Identifier string `json:"identifier"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

func (s *Application) Login(ctx context.Context, client Client, input LoginInput) (Result, error) {
	result := Result{}

	identifier := input.Identifier
	if identifier == "" {
		identifier = input.Email
	}
	email, ok := normalizeLogin(identifier)
	if !ok || len(input.Password) == 0 || len(input.Password) > 512 || !utf8.ValidString(input.Password) {
		return result, fault.New(fault.Unauthenticated, "invalid_credentials")
	}
	if err := s.acquire(); err != nil {
		return result, err
	}
	defer func() { <-s.work }()
	var userID, storedHash, account string
	err := s.store.readLoginCredentials(ctx, nil, email, email, &userID, &storedHash, &account)
	if err != nil && !errors.Is(err, unitofwork.ErrNotFound) {
		return result, fault.New(fault.Unavailable, "login_unavailable")
	}
	if account == "" {
		account = email
	}
	if err := s.limit(ctx, client, account); err != nil {
		return result, err
	}
	if errors.Is(err, unitofwork.ErrNotFound) || !validPasswordHash(storedHash) {
		storedHash = s.dummyHash
	}
	valid := verifyPassword(input.Password, storedHash)
	if !valid || userID == "" {
		return result, fault.New(fault.Unauthenticated, "invalid_credentials")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "login_unavailable")
	}
	defer tx.Rollback()
	var householdID string
	// shortcut: select the first membership until the multi-household selector is implemented.
	// Recheck credentials under the write transaction: recovery may have changed them during hashing.
	err = s.store.readCurrentLoginMembership(ctx, tx, userID, storedHash, &householdID)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return result, fault.New(fault.Unauthenticated, "invalid_credentials")
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "login_unavailable")
	}
	token, err := s.createSession(ctx, tx, client, userID, householdID)
	if err != nil {
		return result, fault.New(fault.Unavailable, "login_unavailable")
	}
	accountKey := sha256.Sum256([]byte("account:" + account))
	if _, err := s.store.clearLoginAccountAttempts(ctx, tx, accountKey[:]); err != nil {
		return result, fault.New(fault.Unavailable, "login_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "login_unavailable")
	}
	result.SessionToken = token

	return result, nil
}

func (s *Application) Logout(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	if client.HasSession {
		digest := sha256.Sum256([]byte(client.SessionToken))
		if _, err := s.store.deleteLoginSession(ctx, nil, digest[:]); err != nil {
			return result, fault.New(fault.Unavailable, "logout_unavailable")
		}
	}
	result.EndSession = true

	return result, nil
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

func (s *Application) acquire() error {
	select {
	case s.work <- struct{}{}:
		return nil
	default:
		return fault.Retry(fault.Busy, "too_many_attempts", 1)
	}
}

// Counters persist across restarts and never trust client-supplied forwarded IPs.
func (s *Application) limit(ctx context.Context, client Client, email string) error {
	ip, _, err := net.SplitHostPort(client.RemoteAddress)
	if err != nil {
		ip = client.RemoteAddress
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return fault.New(fault.Unavailable, "login_unavailable")
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	window := now - int64(attemptWindow.Seconds())
	if _, err := s.store.expireLoginAttempts(ctx, tx, window); err != nil {
		return fault.New(fault.Unavailable, "login_unavailable")
	}
	for _, rule := range []struct {
		key string
		max int
	}{{"ip:" + ip, 50}, {"account:" + email, 10}} {
		digest := sha256.Sum256([]byte(rule.key))
		if _, err := s.store.ensureAttemptCounter(ctx, tx, digest[:], now); err != nil {
			return fault.New(fault.Unavailable, "login_unavailable")
		}
		result, err := s.store.incrementAttemptCounter(ctx, tx, digest[:], rule.max)
		if err != nil {
			return fault.New(fault.Unavailable, "login_unavailable")
		}
		if count, err := result.Affected, result.CountError; err != nil || count == 0 {
			return fault.Retry(fault.Busy, "too_many_attempts", 900)
		}
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.Unavailable, "login_unavailable")
	}
	return nil
}
