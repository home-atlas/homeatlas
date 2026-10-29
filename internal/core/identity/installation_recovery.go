package identity

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
)

var ErrInstallationNotEmpty = errors.New("installation already contains identity")
var ErrInvalidSetupCode = errors.New("invalid installation code")
var ErrRecoveryMembers = errors.New("invalid recovery members")
var recoveryID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var recoveryCapability = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z][a-z0-9.-]{0,63}$`)

// BackupMember is an offline recovery record, never an HTTP account representation.
type BackupMember struct {
	ID, Name, Email, Role, MembershipID string
	Username                            string
	Assisted                            bool
	PasswordHash                        string `json:"-"`
	Permissions                         []string
}

func (s *Service) BackupMembers(ctx context.Context, tx *sql.Tx, household string) ([]BackupMember, error) {
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.name,COALESCE(u.email,''),u.password_hash,m.role,mi.id,COALESCE(u.username,'u-'||lower(u.id)),u.assisted FROM identity_memberships m JOIN identity_users u ON u.id=m.user_id JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id WHERE m.household_id=? ORDER BY u.id`, household)
	if err != nil {
		return nil, err
	}
	var members []BackupMember
	for rows.Next() {
		var m BackupMember
		if err = rows.Scan(&m.ID, &m.Name, &m.Email, &m.PasswordHash, &m.Role, &m.MembershipID, &m.Username, &m.Assisted); err != nil {
			break
		}
		if !validPasswordHash(m.PasswordHash) {
			m.PasswordHash = "!"
		}
		members = append(members, m)
		if len(members) > 1000 {
			err = ErrRecoveryMembers
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range members {
		m, err := s.Member(ctx, tx, household, members[i].ID)
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
		email, ok := optionalEmail(m.Email)
		username, validUsername := normalizeUsername(m.Username)
		if m.Username == "" {
			username, validUsername = "u-"+strings.ToLower(m.ID), true
		}
		if !validUsername || usernames[username] {
			return ErrRecoveryMembers
		}
		usernames[username] = true
		if !recoveryID.MatchString(m.ID) || !recoveryID.MatchString(m.MembershipID) || !validText(m.Name, 80) || !ok || email != m.Email || ids[m.ID] || (m.Email != "" && emails[m.Email]) || membershipIDs[m.MembershipID] || !slices.Contains([]string{"owner", "admin", "member", "restricted", "guest"}, m.Role) || len(m.Permissions) > 64 {
			return ErrRecoveryMembers
		}
		if m.PasswordHash != "!" && !validPasswordHash(m.PasswordHash) {
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

func (s *Service) validSetupCode(code string) bool {
	digest, valid := tokenDigest(strings.TrimSpace(code))
	return valid && len(s.setupCodeHash) > 0 && subtle.ConstantTimeCompare(digest, s.setupCodeHash) == 1
}

func emptyInstallation(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) error {
	var occupied bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_installation) OR EXISTS(SELECT 1 FROM identity_users) OR EXISTS(SELECT 1 FROM identity_households) OR EXISTS(SELECT 1 FROM identity_memberships)`).Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return ErrInstallationNotEmpty
	}
	return nil
}

// Check before accepting an archive; recheck the code and empty state in the committing transaction.
func (s *Service) AuthorizeInstallationRecovery(w http.ResponseWriter, r *http.Request, code string) bool {
	if err := emptyInstallation(r.Context(), s.db); err != nil {
		if errors.Is(err, ErrInstallationNotEmpty) {
			fail(w, 409, "setup_completed")
		} else {
			fail(w, 503, "setup_unavailable")
		}
		return false
	}
	if !s.limit(w, r, "setup") {
		return false
	}
	if !s.validSetupCode(code) {
		fail(w, 403, "invalid_setup_code")
		return false
	}
	return true
}

func (s *Service) ImportInstallation(ctx context.Context, tx *sql.Tx, h Household, members []BackupMember, code string) (access.Principal, error) {
	var owner access.Principal
	if err := emptyInstallation(ctx, tx); err != nil {
		return owner, err
	}
	if !s.validSetupCode(code) {
		return owner, ErrInvalidSetupCode
	}
	if err := ValidateBackupMembers(members); err != nil {
		return owner, err
	}
	if !recoveryID.MatchString(h.ID) || !validText(h.Name, 100) || !validTimezone(h.Timezone) {
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
	if _, err := tx.ExecContext(ctx, "INSERT INTO identity_installation(singleton) VALUES(1)"); err != nil {
		return owner, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO identity_households(id,name,timezone) VALUES(?,?,?)", h.ID, h.Name, h.Timezone); err != nil {
		return owner, err
	}
	for _, m := range members {
		if _, err := tx.ExecContext(ctx, "INSERT INTO identity_users(id,name,email,password_hash,username,assisted) VALUES(?,?,?,?,?,?)", m.ID, m.Name, nullableEmail(m.Email), m.PasswordHash, restoredUsername(m), m.Assisted); err != nil {
			return owner, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO identity_memberships(user_id,household_id,role) VALUES(?,?,?)", m.ID, h.ID, m.Role); err != nil {
			return owner, err
		}
		for _, permission := range m.Permissions {
			if _, err := tx.ExecContext(ctx, "INSERT INTO identity_permissions(user_id,household_id,capability) VALUES(?,?,?)", m.ID, h.ID, permission); err != nil {
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
func (s *Service) FinishInstallationRecovery() { s.removeSetupCode() }

func restoredUsername(m BackupMember) string {
	if m.Username != "" {
		return m.Username
	}
	return "u-" + strings.ToLower(m.ID)
}
