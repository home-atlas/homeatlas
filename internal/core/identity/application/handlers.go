package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

func (s *Application) Initialized(ctx context.Context) (bool, error) {
	var initialized bool
	err := s.store.ReadInitialized(ctx, nil, &initialized)
	return initialized, err
}

func (s *Application) Session(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	ready, err := s.Initialized(ctx)
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

	ready, err := s.Initialized(ctx)
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
	email, ok := domain.OptionalEmail(input.Email)
	username, userOK := domain.NormalizeUsername(input.Username)
	if input.Username == "" && email != "" {
		username, userOK = "u-"+strings.ToLower(rand.Text()), true
	}
	if !domain.ValidTimezone(input.Timezone) {
		return result, fault.New(fault.Invalid, "invalid_setup")
	}
	if !ok || !userOK || !domain.ValidText(input.Name, 80) || !domain.ValidText(input.HouseholdName, 100) || !domain.ValidNewPassword(input.Password) {
		return result, fault.New(fault.Invalid, "invalid_setup")
	}
	if err := s.acquire(); err != nil {
		return result, err
	}
	defer func() { <-s.work }()
	if err := s.limit(ctx, client, email); err != nil {
		return result, err
	}
	hash, err := domain.HashPassword(input.Password)
	if err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	defer tx.Rollback()
	mutation, err := s.store.ClaimInstallation(ctx, tx)
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
	if _, err := s.store.InsertOwnerAccount(ctx, tx, userID, input.Name, nullableEmail(email), hash, username); err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	if _, err := s.store.InsertOwnerHousehold(ctx, tx, householdID, input.HouseholdName, input.Timezone); err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	if _, err := s.store.InsertOwnerMembership(ctx, tx, userID, householdID); err != nil {
		return result, fault.New(fault.Internal, "setup_unavailable")
	}
	for _, capability := range s.ownerCapabilities {
		if _, err := s.store.InsertOwnerCapability(ctx, tx, userID, householdID, capability); err != nil {
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
	email, ok := domain.NormalizeLogin(identifier)
	if !ok || len(input.Password) == 0 || len(input.Password) > 512 || !utf8.ValidString(input.Password) {
		return result, fault.New(fault.Unauthenticated, "invalid_credentials")
	}
	if err := s.acquire(); err != nil {
		return result, err
	}
	defer func() { <-s.work }()
	var userID, storedHash, account string
	err := s.store.ReadLoginCredentials(ctx, nil, email, email, &userID, &storedHash, &account)
	if err != nil && !errors.Is(err, unitofwork.ErrNotFound) {
		return result, fault.New(fault.Unavailable, "login_unavailable")
	}
	if account == "" {
		account = email
	}
	if err := s.limit(ctx, client, account); err != nil {
		return result, err
	}
	if errors.Is(err, unitofwork.ErrNotFound) || !domain.ValidPasswordHash(storedHash) {
		storedHash = s.dummyHash
	}
	valid := domain.VerifyPassword(input.Password, storedHash)
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
	err = s.store.ReadCurrentLoginMembership(ctx, tx, userID, storedHash, &householdID)
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
	if _, err := s.store.ClearLoginAccountAttempts(ctx, tx, accountKey[:]); err != nil {
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
		if _, err := s.store.DeleteLoginSession(ctx, nil, digest[:]); err != nil {
			return result, fault.New(fault.Unavailable, "logout_unavailable")
		}
	}
	result.EndSession = true

	return result, nil
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
	window := now - int64(domain.AttemptWindow.Seconds())
	if _, err := s.store.ExpireLoginAttempts(ctx, tx, window); err != nil {
		return fault.New(fault.Unavailable, "login_unavailable")
	}
	for _, rule := range []struct {
		key string
		max int
	}{{"ip:" + ip, 50}, {"account:" + email, 10}} {
		digest := sha256.Sum256([]byte(rule.key))
		if _, err := s.store.EnsureAttemptCounter(ctx, tx, digest[:], now); err != nil {
			return fault.New(fault.Unavailable, "login_unavailable")
		}
		result, err := s.store.IncrementAttemptCounter(ctx, tx, digest[:], rule.max)
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
