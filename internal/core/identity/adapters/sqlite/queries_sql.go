package sqlite

import (
	"context"
	"database/sql"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository struct{ db *sql.DB }

func (s *Repository) Begin(ctx context.Context) (unitofwork.Transaction, error) {
	return s.db.BeginTx(ctx, nil)
}
func (s *Repository) ReadPasswordProof(ctx context.Context, tx unitofwork.Transaction, userID string, stored *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, "SELECT password_hash FROM identity_users WHERE id=?", userID).Scan(stored)
}

func (s *Repository) IsPasswordProofCurrent(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, householdID string, now int64, lastSeenAfter int64, stored string, membershipID string, valid *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_sessions s JOIN identity_users u ON u.id=s.user_id JOIN identity_member_ids m ON m.user_id=s.user_id AND m.household_id=s.household_id WHERE s.token_hash=? AND s.user_id=? AND s.household_id=? AND s.expires_at>? AND s.last_seen>? AND u.password_hash=? AND m.id=?)`, digest, userID, householdID, now, lastSeenAfter, stored, membershipID).Scan(valid)
}

func (s *Repository) AssistedAccountExists(ctx context.Context, tx unitofwork.Transaction, username string, email *string, exists *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_users WHERE username=? OR email=?)", username, email).Scan(exists)
}

func (s *Repository) InsertAssistedAccount(ctx context.Context, tx unitofwork.Transaction, user string, name string, email *string, username string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_users(id,name,email,password_hash,username,assisted) VALUES(?,?,?,'!',?,1)", user, name, email, username))
}

func (s *Repository) InsertAssistedMembership(ctx context.Context, tx unitofwork.Transaction, user string, householdID string, role string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_memberships(user_id,household_id,role) VALUES(?,?,?)", user, householdID, role))
}

func (s *Repository) SaveAssistedEnrollment(ctx context.Context, tx unitofwork.Transaction, user string, householdID string, digest []byte, issuerID string, membershipID string, expiresAt int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_assisted_enrollments(user_id,household_id,token_hash,issuer_id,issuer_member_id,expires_at) VALUES(?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET household_id=excluded.household_id,token_hash=excluded.token_hash,issuer_id=excluded.issuer_id,issuer_member_id=excluded.issuer_member_id,expires_at=excluded.expires_at`, user, householdID, digest, issuerID, membershipID, expiresAt))
}

func (s *Repository) RecordEnrollmentIssued(ctx context.Context, tx unitofwork.Transaction, id string, user string, householdID string, actorID string, now int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_access_events(id,user_id,household_id,actor_id,action,created_at) VALUES(?,?,?,?,'enrollment_issued',?)`, id, user, householdID, actorID, now))
}

func (s *Repository) IsAssistedAccount(ctx context.Context, tx unitofwork.Transaction, userID string, assisted *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, "SELECT assisted=1 AND password_hash='!' FROM identity_users WHERE id=?", userID).Scan(assisted)
}

func (s *Repository) ReadEnrollment(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, user *string, HouseholdID *string, name *string, username *string, IssuerID *string, IssuerMemberID *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT e.user_id,e.household_id,u.name,u.username,e.issuer_id,e.issuer_member_id FROM identity_assisted_enrollments e JOIN identity_users u ON u.id=e.user_id JOIN identity_memberships m ON m.user_id=e.user_id AND m.household_id=e.household_id JOIN identity_member_ids i ON i.user_id=e.issuer_id AND i.household_id=e.household_id AND i.id=e.issuer_member_id WHERE e.token_hash=? AND e.expires_at>? AND u.assisted=1 AND u.password_hash='!' AND m.role IN ('member','restricted','guest') AND NOT EXISTS(SELECT 1 FROM identity_permissions p WHERE p.user_id=m.user_id AND p.household_id=m.household_id AND p.capability LIKE 'household.%')`, digest, now).Scan(user, HouseholdID, name, username, IssuerID, IssuerMemberID)
}

func (s *Repository) RecordDeviceActivated(ctx context.Context, tx unitofwork.Transaction, id string, user string, HouseholdID string, IssuerID string, now int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_access_events(id,user_id,household_id,actor_id,action,created_at) VALUES(?,?,?,?,'device_activated',?)`, id, user, HouseholdID, IssuerID, now))
}

func (s *Repository) DeletePreviousPINBinding(ctx context.Context, tx unitofwork.Transaction, digest []byte) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "DELETE FROM identity_pin_devices WHERE token_hash=?", digest))
}

func (s *Repository) CountPINDevices(ctx context.Context, tx unitofwork.Transaction, user string, count *int) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, "SELECT count(*) FROM identity_pin_devices WHERE user_id=?", user).Scan(count)
}

func (s *Repository) InsertDeviceBinding(ctx context.Context, tx unitofwork.Transaction, id string, user string, HouseholdID string, digest []byte, hash string, name string, createdAt int64, expiresAt int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_pin_devices(id,user_id,household_id,token_hash,pin_hash,name,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`, id, user, HouseholdID, digest, hash, name, createdAt, expiresAt))
}

func (s *Repository) ReadDeviceBinding(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, maximumFailures int, id *string, user *string, HouseholdID *string, hash *string, name *string, username *string, passwordHash *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT d.id,d.user_id,d.household_id,d.pin_hash,u.name,COALESCE(u.username,'u-'||lower(u.id)),u.password_hash FROM identity_pin_devices d JOIN identity_users u ON u.id=d.user_id JOIN identity_memberships m ON m.user_id=d.user_id AND m.household_id=d.household_id WHERE d.token_hash=? AND d.expires_at>? AND d.failures<? AND m.role IN ('member','restricted','guest') AND NOT EXISTS(SELECT 1 FROM identity_permissions p WHERE p.user_id=m.user_id AND p.household_id=m.household_id AND p.capability LIKE 'household.%')`, digest, now, maximumFailures).Scan(id, user, HouseholdID, hash, name, username, passwordHash)
}

func (s *Repository) RecordPINFailure(ctx context.Context, tx unitofwork.Transaction, id string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "UPDATE identity_pin_devices SET failures=failures+1 WHERE id=?", id))
}

func (s *Repository) MarkPINSession(ctx context.Context, tx unitofwork.Transaction, digest []byte) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_pin_sessions(token_hash) VALUES(?)", digest))
}

func (s *Repository) ResetPINFailures(ctx context.Context, tx unitofwork.Transaction, id string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "UPDATE identity_pin_devices SET failures=0 WHERE id=?", id))
}

func (s *Repository) ReadAccountAccess(ctx context.Context, tx unitofwork.Transaction, userID string, username *string, hasPassword *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, "SELECT COALESCE(username,'u-'||lower(id)), password_hash!='!' FROM identity_users WHERE id=?", userID).Scan(username, hasPassword)
}

func (s *Repository) SaveUsername(ctx context.Context, tx unitofwork.Transaction, username string, userID string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "UPDATE identity_users SET username=? WHERE id=?", username, userID))
}

func (s *Repository) DeleteDeviceBinding(ctx context.Context, tx unitofwork.Transaction, target string, userID string, householdID string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "DELETE FROM identity_pin_devices WHERE id=? AND user_id=? AND household_id=?", target, userID, householdID))
}

func (s *Repository) RevokeDeviceSessions(ctx context.Context, tx unitofwork.Transaction, userID string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "DELETE FROM identity_sessions WHERE user_id=?", userID))
}

func (s *Repository) ReadVerificationMailbox(ctx context.Context, tx unitofwork.Transaction, userID string, digest []byte, now int64, lastSeenAfter int64, email *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT u.email FROM identity_users u JOIN identity_sessions s ON s.user_id=u.id
 WHERE u.id=? AND s.token_hash=? AND s.expires_at>? AND s.last_seen>?`, userID, digest, now, lastSeenAfter).Scan(email)
}

func (s *Repository) SaveMailboxVerification(ctx context.Context, tx unitofwork.Transaction, userID string, email string, hash []byte, expiresAt int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_email_verification(user_id,email,token_hash,expires_at) VALUES(?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,token_hash=excluded.token_hash,expires_at=excluded.expires_at`, userID, email, hash, expiresAt))
}

func (s *Repository) ConsumeMailboxVerification(ctx context.Context, tx unitofwork.Transaction, hash []byte, now int64, user *string, email *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `DELETE FROM identity_email_verification WHERE token_hash=? AND expires_at>?
 AND EXISTS(SELECT 1 FROM identity_users u JOIN identity_memberships m ON m.user_id=u.id WHERE u.id=identity_email_verification.user_id AND u.email=identity_email_verification.email)
 RETURNING user_id,email`, hash, now).Scan(user, email)
}

func (s *Repository) SaveVerifiedMailbox(ctx context.Context, tx unitofwork.Transaction, user string, email string, now int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_verified_emails(user_id,email,verified_at) VALUES(?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,verified_at=excluded.verified_at`, user, email, now))
}

func (s *Repository) ReadVerifiedRecoveryAccount(ctx context.Context, tx unitofwork.Transaction, email string, user *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT u.id FROM identity_users u JOIN identity_verified_emails v ON v.user_id=u.id AND v.email=u.email
 WHERE u.email=? AND EXISTS(SELECT 1 FROM identity_memberships m WHERE m.user_id=u.id)`, email).Scan(user)
}

func (s *Repository) SaveMailRecovery(ctx context.Context, tx unitofwork.Transaction, user string, email string, hash []byte, expiresAt int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_mail_recovery(user_id,email,token_hash,expires_at) VALUES(?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,token_hash=excluded.token_hash,expires_at=excluded.expires_at`, user, email, hash, expiresAt))
}

func (s *Repository) ReadInitialized(ctx context.Context, tx unitofwork.Transaction, initialized *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_installation)").Scan(initialized)
}

func (s *Repository) ClaimInstallation(ctx context.Context, tx unitofwork.Transaction) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_installation(singleton) VALUES (1) ON CONFLICT DO NOTHING"))
}

func (s *Repository) InsertOwnerAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email *string, hash string, username string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_users (id, name, email, password_hash, username) VALUES (?, ?, ?, ?, ?)", userID, name, email, hash, username))
}

func (s *Repository) InsertOwnerHousehold(ctx context.Context, tx unitofwork.Transaction, householdID string, HouseholdName string, timezone string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_households (id, name, timezone) VALUES (?, ?, ?)", householdID, HouseholdName, timezone))
}

func (s *Repository) InsertOwnerMembership(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_memberships (user_id, household_id, role) VALUES (?, ?, 'owner')", userID, householdID))
}

func (s *Repository) InsertOwnerCapability(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, capability string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_permissions (user_id, household_id, capability) VALUES (?, ?, ?)", userID, householdID, capability))
}

func (s *Repository) ReadLoginCredentials(ctx context.Context, tx unitofwork.Transaction, email string, email2 string, userID *string, storedHash *string, account *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, "SELECT id, password_hash, COALESCE(email,username) FROM identity_users WHERE email = ? OR username = ?", email, email2).Scan(userID, storedHash, account)
}

func (s *Repository) ReadCurrentLoginMembership(ctx context.Context, tx unitofwork.Transaction, userID string, storedHash string, householdID *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT m.household_id FROM identity_memberships m JOIN identity_users u ON u.id=m.user_id
 WHERE m.user_id=? AND u.password_hash=? ORDER BY m.household_id LIMIT 1`, userID, storedHash).Scan(householdID)
}

func (s *Repository) ClearLoginAccountAttempts(ctx context.Context, tx unitofwork.Transaction, accountKey []byte) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "DELETE FROM identity_login_attempts WHERE key = ?", accountKey))
}

func (s *Repository) DeleteLoginSession(ctx context.Context, tx unitofwork.Transaction, digest []byte) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "DELETE FROM identity_sessions WHERE token_hash = ?", digest))
}

func (s *Repository) ExpireLoginAttempts(ctx context.Context, tx unitofwork.Transaction, window int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "DELETE FROM identity_login_attempts WHERE window_start <= ?", window))
}

func (s *Repository) EnsureAttemptCounter(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_login_attempts (key, window_start, attempts) VALUES (?, ?, 0) ON CONFLICT DO NOTHING", digest, now))
}

func (s *Repository) IncrementAttemptCounter(ctx context.Context, tx unitofwork.Transaction, digest []byte, max int) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "UPDATE identity_login_attempts SET attempts = attempts + 1 WHERE key = ? AND attempts < ?", digest, max))
}

func (s *Repository) TouchSession(ctx context.Context, tx unitofwork.Transaction, now int64, digest []byte) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "UPDATE identity_sessions SET last_seen = MAX(last_seen, ?) WHERE token_hash = ?", now, digest))
}

func (s *Repository) RotateLoginSession(ctx context.Context, tx unitofwork.Transaction, old []byte) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "DELETE FROM identity_sessions WHERE token_hash = ?", old))
}

func (s *Repository) ExpireLoginSessions(ctx context.Context, tx unitofwork.Transaction, now int64, lastSeenBefore int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "DELETE FROM identity_sessions WHERE expires_at <= ? OR last_seen <= ?", now, lastSeenBefore))
}

func (s *Repository) InsertLoginSession(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, householdID string, expiresAt int64, now int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_sessions (token_hash, user_id, household_id, expires_at, last_seen) VALUES (?, ?, ?, ?, ?)", digest, userID, householdID, expiresAt, now))
}

func (s *Repository) ReadInstallationOccupied(ctx context.Context, tx unitofwork.Transaction, occupied *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_installation) OR EXISTS(SELECT 1 FROM identity_users) OR EXISTS(SELECT 1 FROM identity_households) OR EXISTS(SELECT 1 FROM identity_memberships)`).Scan(occupied)
}

func (s *Repository) RestoreInstallation(ctx context.Context, tx unitofwork.Transaction) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_installation(singleton) VALUES(1)"))
}

func (s *Repository) RestoreHousehold(ctx context.Context, tx unitofwork.Transaction, householdID string, name string, timezone string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_households(id,name,timezone) VALUES(?,?,?)", householdID, name, timezone))
}

func (s *Repository) RestoreAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email *string, passwordHash string, username string, assisted bool) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_users(id,name,email,password_hash,username,assisted) VALUES(?,?,?,?,?,?)", userID, name, email, passwordHash, username, assisted))
}

func (s *Repository) RestoreMembership(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, role string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_memberships(user_id,household_id,role) VALUES(?,?,?)", userID, householdID, role))
}

func (s *Repository) RestoreCapability(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, permission string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, "INSERT INTO identity_permissions(user_id,household_id,capability) VALUES(?,?,?)", userID, householdID, permission))
}

func (s *Repository) IsExistingHouseholdMember(ctx context.Context, tx unitofwork.Transaction, householdID string, email string, exists *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_memberships m JOIN identity_users u ON u.id=m.user_id WHERE m.household_id=? AND u.email=?)`, householdID, email).Scan(exists)
}

func (s *Repository) HasProtectedAdminInvitation(ctx context.Context, tx unitofwork.Transaction, householdID string, email string, now int64, protected *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_invitations WHERE household_id=? AND email=? AND role='admin' AND accepted_at=0 AND revoked_at=0 AND expires_at>?)`, householdID, email, now).Scan(protected)
}

func (s *Repository) RevokePreviousInvitations(ctx context.Context, tx unitofwork.Transaction, now int64, householdID string, email string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `UPDATE identity_invitations SET revoked_at=? WHERE household_id=? AND email=? AND accepted_at=0 AND revoked_at=0`, now, householdID, email))
}

func (s *Repository) InsertInvitation(ctx context.Context, tx unitofwork.Transaction, id string, householdID string, email string, role string, encoded string, digest []byte, issuerID string, membershipID string, now int64, expiresAt int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_invitations(id,household_id,email,role,permissions,token_hash,issuer_id,issuer_member_id,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, householdID, email, role, encoded, digest, issuerID, membershipID, now, expiresAt))
}

func (s *Repository) ReadInvitationForRevocation(ctx context.Context, tx unitofwork.Transaction, target string, householdID string) (stored0 Invitation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return readInvitation(source.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM identity_invitations i JOIN identity_households h ON h.id=i.household_id WHERE i.id=? AND i.household_id=?`, target, householdID))
}

func (s *Repository) RevokeInvitationRecord(ctx context.Context, tx unitofwork.Transaction, now int64, id string, householdID string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `UPDATE identity_invitations SET revoked_at=? WHERE id=? AND household_id=? AND accepted_at=0`, now, id, householdID))
}

func (s *Repository) ReadInvitationByDigest(ctx context.Context, tx unitofwork.Transaction, digest []byte) (stored0 Invitation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return readInvitation(source.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM identity_invitations i JOIN identity_households h ON h.id=i.household_id WHERE i.token_hash=?`, digest))
}

func (s *Repository) InvitationAccountExists(ctx context.Context, tx unitofwork.Transaction, email string, exists *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_users WHERE email=?)`, email).Scan(exists)
}

func (s *Repository) ReadInvitedAccount(ctx context.Context, tx unitofwork.Transaction, email string, userID *string, stored *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT id,password_hash FROM identity_users WHERE email=?`, email).Scan(userID, stored)
}

func (s *Repository) InvitedAccountUnchanged(ctx context.Context, tx unitofwork.Transaction, userID string, stored string, unchanged *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_users WHERE id=? AND password_hash=?)`, userID, stored).Scan(unchanged)
}

func (s *Repository) IsInvitedSessionActive(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, now int64, lastSeenAfter int64, active *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_sessions WHERE token_hash=? AND user_id=? AND expires_at>? AND last_seen>?)`, digest, userID, now, lastSeenAfter).Scan(active)
}

func (s *Repository) IsInvitedMailboxTaken(ctx context.Context, tx unitofwork.Transaction, email string, exists *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_users WHERE email=?)`, email).Scan(exists)
}

func (s *Repository) InsertInvitedAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email string, stored string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_users(id,name,email,password_hash) VALUES(?,?,?,?)`, userID, name, email, stored))
}

func (s *Repository) HasInvitedMembership(ctx context.Context, tx unitofwork.Transaction, HouseholdID string, userID string, exists *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_memberships WHERE household_id=? AND user_id=?)`, HouseholdID, userID).Scan(exists)
}

func (s *Repository) InsertInvitedMembership(ctx context.Context, tx unitofwork.Transaction, userID string, HouseholdID string, role string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_memberships VALUES(?,?,?)`, userID, HouseholdID, role))
}

func (s *Repository) ClaimInvitation(ctx context.Context, tx unitofwork.Transaction, now int64, id string, validAfter int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `UPDATE identity_invitations SET accepted_at=? WHERE id=? AND accepted_at=0 AND revoked_at=0 AND expires_at>?`, now, id, validAfter))
}

func (s *Repository) ClearMemberCapabilities(ctx context.Context, tx unitofwork.Transaction, HouseholdID string, user string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `DELETE FROM identity_permissions WHERE household_id=? AND user_id=?`, HouseholdID, user))
}

func (s *Repository) InsertMemberCapability(ctx context.Context, tx unitofwork.Transaction, user string, HouseholdID string, cap string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_permissions VALUES(?,?,?)`, user, HouseholdID, cap))
}

func (s *Repository) DeleteMembership(ctx context.Context, tx unitofwork.Transaction, householdID string, userID string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `DELETE FROM identity_memberships WHERE household_id=? AND user_id=?`, householdID, userID))
}

func (s *Repository) SaveMembershipRole(ctx context.Context, tx unitofwork.Transaction, role string, householdID string, userID string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `UPDATE identity_memberships SET role=? WHERE household_id=? AND user_id=?`, role, householdID, userID))
}

func (s *Repository) RevokeMemberInvitations(ctx context.Context, tx unitofwork.Transaction, now int64, householdID string, userID string, email string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `UPDATE identity_invitations SET revoked_at=? WHERE household_id=? AND accepted_at=0 AND revoked_at=0 AND (issuer_id=? OR email=?)`, now, householdID, userID, email))
}

func (s *Repository) ReadSetupState(ctx context.Context, tx unitofwork.Transaction, ready *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_installation)").Scan(ready)
}

func (s *Repository) ReadLocalRecoveryAccount(ctx context.Context, tx unitofwork.Transaction, email string, email2 string, userID *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT u.id FROM identity_users u WHERE (u.email=? OR u.username=?)
 AND EXISTS(SELECT 1 FROM identity_installation)
 AND EXISTS(SELECT 1 FROM identity_memberships m WHERE m.user_id=u.id)`, email, email2).Scan(userID)
}

func (s *Repository) SaveLocalRecovery(ctx context.Context, tx unitofwork.Transaction, userID string, digest []byte, expiresAt int64) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `INSERT INTO identity_recovery(user_id,token_hash,expires_at) VALUES(?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET token_hash=excluded.token_hash,expires_at=excluded.expires_at`, userID, digest, expiresAt))
}

func (s *Repository) IsRecoveryAvailable(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, digest2 []byte, now2 int64, exists *bool) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_recovery WHERE token_hash=? AND expires_at>?) OR EXISTS(`+validMailRecovery+`)`, digest, now, digest2, now2).Scan(exists)
}

func (s *Repository) ConsumeLocalRecovery(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, userID *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `DELETE FROM identity_recovery WHERE token_hash=? AND expires_at>?
 AND EXISTS(SELECT 1 FROM identity_memberships m WHERE m.user_id=identity_recovery.user_id) RETURNING user_id`, digest, now).Scan(userID)
}

func (s *Repository) ConsumeMailRecovery(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, digest2 []byte, userID *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `DELETE FROM identity_mail_recovery WHERE user_id IN (`+validMailRecovery+`) AND token_hash=? RETURNING user_id`, digest, now, digest2).Scan(userID)
}

func (s *Repository) ReplaceRecoveredPassword(ctx context.Context, tx unitofwork.Transaction, hash string, userID string, email *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `UPDATE identity_users SET password_hash=? WHERE id=? RETURNING COALESCE(email,username)`, hash, userID).Scan(email)
}

func (s *Repository) ClearRecoveredAccountAttempts(ctx context.Context, tx unitofwork.Transaction, accountKey []byte) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `DELETE FROM identity_login_attempts WHERE key=?`, accountKey))
}

func (s *Repository) ReadHouseholdSettings(ctx context.Context, tx unitofwork.Transaction, householdID string, id *string, name *string, timezone *string) (adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return source.QueryRowContext(ctx, `SELECT id,name,timezone FROM identity_households WHERE id=?`, householdID).Scan(id, name, timezone)
}

func (s *Repository) SaveHouseholdSettings(ctx context.Context, tx unitofwork.Transaction, name string, timezone string, householdID string) (mutation unitofwork.Mutation, adapterErr error) {
	source, adapterErr := database.Within(s.db, tx)
	if adapterErr != nil {
		return
	}
	defer func() { adapterErr = database.Normalize(adapterErr) }()
	return database.Mutation(source.ExecContext(ctx, `UPDATE identity_households SET name=?,timezone=? WHERE id=?`, name, timezone, householdID))
}

func (s *Repository) RevokeAssistedCredentials(ctx context.Context, tx unitofwork.Transaction, user string) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	for _, statement := range []string{"DELETE FROM identity_pin_devices WHERE user_id=?", "DELETE FROM identity_sessions WHERE user_id=?", "DELETE FROM identity_assisted_enrollments WHERE user_id=?"} {
		if _, err := q.ExecContext(ctx, statement, user); err != nil {
			return err
		}
	}
	return nil
}
func (s *Repository) RevokeRecoveredCredentials(ctx context.Context, tx unitofwork.Transaction, user string, now int64) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	for _, statement := range []string{"DELETE FROM identity_recovery WHERE user_id=?", "DELETE FROM identity_mail_recovery WHERE user_id=?", "DELETE FROM identity_email_verification WHERE user_id=?", "DELETE FROM identity_sessions WHERE user_id=?", "DELETE FROM identity_pin_devices WHERE user_id=?", "DELETE FROM identity_assisted_enrollments WHERE user_id=?"} {
		if _, err := q.ExecContext(ctx, statement, user); err != nil {
			return err
		}
	}
	_, err = q.ExecContext(ctx, `UPDATE identity_invitations SET revoked_at=? WHERE issuer_id=? AND accepted_at=0 AND revoked_at=0`, now, user)
	return err
}

const validMailRecovery = `SELECT t.user_id FROM identity_mail_recovery t
 JOIN identity_users u ON u.id=t.user_id AND u.email=t.email
 JOIN identity_verified_emails v ON v.user_id=u.id AND v.email=u.email
 WHERE t.token_hash=? AND t.expires_at>?
 AND EXISTS(SELECT 1 FROM identity_memberships m WHERE m.user_id=u.id)`
