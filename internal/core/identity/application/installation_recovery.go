package application

import (
	"context"
	"crypto/subtle"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

var ErrInstallationNotEmpty = errors.New("installation already contains identity")
var ErrInvalidSetupCode = errors.New("invalid installation code")
var ErrRecoveryMembers = errors.New("invalid recovery members")
var recoveryID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var recoveryCapability = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z][a-z0-9.-]{0,63}$`)

// BackupMember is an offline recovery record, never an HTTP account representation.

func (s *Application) BackupMembers(ctx context.Context, tx unitofwork.Transaction, HouseholdID string) ([]BackupMember, error) {
	members, err := s.store.ReadBackupMemberRecords(ctx, tx, HouseholdID, 1000)
	if err != nil {
		return nil, err
	}
	if len(members) > 1000 {
		return nil, ErrRecoveryMembers
	}
	for i := range members {
		if !domain.ValidPasswordHash(members[i].PasswordHash) {
			members[i].PasswordHash = "!"
		}
	}
	for i := range members {
		m, err := s.Member(ctx, tx, HouseholdID, members[i].ID)
		if err != nil {
			return nil, err
		}
		members[i].Permissions = m.Permissions
	}
	return members, ValidateBackupMembers(members)
}

func ValidateBackupMembers(members []BackupMember) error {
	if len(members) == 0 || len(members) > 1000 {
		return ErrRecoveryMembers
	}
	usernames := map[string]bool{}
	ids, emails, membershipIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	owners := 0
	for _, m := range members {
		email, ok := domain.OptionalEmail(m.Email)
		username, validUsername := domain.NormalizeUsername(m.Username)
		if m.Username == "" {
			username, validUsername = "u-"+strings.ToLower(m.ID), true
		}
		if !validUsername || usernames[username] {
			return ErrRecoveryMembers
		}
		usernames[username] = true
		if !recoveryID.MatchString(m.ID) || !recoveryID.MatchString(m.MembershipID) || !domain.ValidText(m.Name, 80) || !ok || email != m.Email || ids[m.ID] || (m.Email != "" && emails[m.Email]) || membershipIDs[m.MembershipID] || !slices.Contains([]string{"owner", "admin", "member", "restricted", "guest"}, m.Role) || len(m.Permissions) > 64 {
			return ErrRecoveryMembers
		}
		if m.PasswordHash != "!" && !domain.ValidPasswordHash(m.PasswordHash) {
			return ErrRecoveryMembers
		}
		ids[m.ID] = true
		emails[m.Email] = true
		membershipIDs[m.MembershipID] = true
		if m.Role == "owner" {
			owners++
		}
		seen := map[string]bool{}
		for _, permission := range m.Permissions {
			if !recoveryCapability.MatchString(permission) || seen[permission] {
				return ErrRecoveryMembers
			}
			seen[permission] = true
		}
	}
	if owners != 1 {
		return ErrRecoveryMembers
	}
	return nil
}

func (s *Application) validSetupCode(code string) bool {
	digest, valid := domain.TokenDigest(strings.TrimSpace(code))
	return valid && len(s.setupCodeHash) > 0 && subtle.ConstantTimeCompare(digest, s.setupCodeHash) == 1
}

func (s *Application) emptyInstallation(ctx context.Context, tx unitofwork.Transaction) error {
	var occupied bool
	if err := s.store.ReadInstallationOccupied(ctx, tx, &occupied); err != nil {
		return err
	}
	if occupied {
		return ErrInstallationNotEmpty
	}
	return nil
}

// Check before accepting an archive; recheck the code and empty state in the committing transaction.
func (s *Application) CheckInstallationRecovery(ctx context.Context, client Client, code string) error {
	if err := s.emptyInstallation(ctx, nil); err != nil {
		if errors.Is(err, ErrInstallationNotEmpty) {
			return fault.New(fault.Conflict, "setup_completed")
		} else {
			return fault.New(fault.Unavailable, "setup_unavailable")
		}
	}
	if err := s.limit(ctx, client, "setup"); err != nil {
		return err
	}
	if !s.validSetupCode(code) {
		return fault.New(fault.Denied, "invalid_setup_code")
	}
	return nil
}

func (s *Application) ImportInstallation(ctx context.Context, tx unitofwork.Transaction, h Household, members []BackupMember, code string) (access.Principal, error) {
	var owner access.Principal
	if err := s.emptyInstallation(ctx, tx); err != nil {
		return owner, err
	}
	if !s.validSetupCode(code) {
		return owner, ErrInvalidSetupCode
	}
	if err := ValidateBackupMembers(members); err != nil {
		return owner, err
	}
	if !recoveryID.MatchString(h.ID) || !domain.ValidText(h.Name, 100) || !domain.ValidTimezone(h.Timezone) {
		return owner, ErrRecoveryMembers
	}
	allowed := append(append([]string{}, s.ownerCapabilities...), s.capabilities...)
	for _, m := range members {
		for _, permission := range m.Permissions {
			if !slices.Contains(allowed, permission) {
				return owner, ErrRecoveryMembers
			}
		}
	}
	if _, err := s.store.RestoreInstallation(ctx, tx); err != nil {
		return owner, err
	}
	if _, err := s.store.RestoreHousehold(ctx, tx, h.ID, h.Name, h.Timezone); err != nil {
		return owner, err
	}
	for _, m := range members {
		if _, err := s.store.RestoreAccount(ctx, tx, m.ID, m.Name, nullableEmail(m.Email), m.PasswordHash, domain.RestoredUsername(m), m.Assisted); err != nil {
			return owner, err
		}
		if _, err := s.store.RestoreMembership(ctx, tx, m.ID, h.ID, m.Role); err != nil {
			return owner, err
		}
		for _, permission := range m.Permissions {
			if _, err := s.store.RestoreCapability(ctx, tx, m.ID, h.ID, permission); err != nil {
				return owner, err
			}
		}
		if m.Role == "owner" {
			owner = access.Principal{UserID: m.ID, HouseholdID: h.ID}
		}
	}
	return owner, nil
}

// The committed singleton prevents reuse even if cleanup is interrupted.
func (s *Application) FinishInstallationRecovery() { s.removeSetupCode() }
