package identity

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

// Repository owns identity persistence. Every transactional operation receives the same committing scope.
type Repository interface {
	revokeAssistedCredentials(context.Context, unitofwork.Transaction, string) error
	revokeRecoveredCredentials(context.Context, unitofwork.Transaction, string, int64) error
	Begin(context.Context) (unitofwork.Transaction, error)
	readPasswordProof(ctx context.Context, tx unitofwork.Transaction, userID string, stored *string) error
	isPasswordProofCurrent(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, householdID string, now int64, lastSeenAfter int64, stored string, membershipID string, valid *bool) error
	assistedAccountExists(ctx context.Context, tx unitofwork.Transaction, username string, email *string, exists *bool) error
	insertAssistedAccount(ctx context.Context, tx unitofwork.Transaction, user string, name string, email *string, username string) (unitofwork.Mutation, error)
	insertAssistedMembership(ctx context.Context, tx unitofwork.Transaction, user string, householdID string, role string) (unitofwork.Mutation, error)
	saveAssistedEnrollment(ctx context.Context, tx unitofwork.Transaction, user string, householdID string, digest []byte, issuerID string, membershipID string, expiresAt int64) (unitofwork.Mutation, error)
	recordEnrollmentIssued(ctx context.Context, tx unitofwork.Transaction, id string, user string, householdID string, actorID string, now int64) (unitofwork.Mutation, error)
	isAssistedAccount(ctx context.Context, tx unitofwork.Transaction, userID string, assisted *bool) error
	readEnrollment(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, user *string, household *string, name *string, username *string, issuer *string, issuerMember *string) error
	recordDeviceActivated(ctx context.Context, tx unitofwork.Transaction, id string, user string, household string, issuer string, now int64) (unitofwork.Mutation, error)
	deletePreviousPINBinding(ctx context.Context, tx unitofwork.Transaction, digest []byte) (unitofwork.Mutation, error)
	countPINDevices(ctx context.Context, tx unitofwork.Transaction, user string, count *int) error
	insertDeviceBinding(ctx context.Context, tx unitofwork.Transaction, id string, user string, household string, digest []byte, hash string, name string, createdAt int64, expiresAt int64) (unitofwork.Mutation, error)
	readDeviceBinding(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, maximumFailures int, id *string, user *string, household *string, hash *string, name *string, username *string, passwordHash *string) error
	recordPINFailure(ctx context.Context, tx unitofwork.Transaction, id string) (unitofwork.Mutation, error)
	markPINSession(ctx context.Context, tx unitofwork.Transaction, digest []byte) (unitofwork.Mutation, error)
	resetPINFailures(ctx context.Context, tx unitofwork.Transaction, id string) (unitofwork.Mutation, error)
	readAccountAccess(ctx context.Context, tx unitofwork.Transaction, userID string, username *string, hasPassword *bool) error
	saveUsername(ctx context.Context, tx unitofwork.Transaction, username string, userID string) (unitofwork.Mutation, error)
	deleteDeviceBinding(ctx context.Context, tx unitofwork.Transaction, target string, userID string, householdID string) (unitofwork.Mutation, error)
	revokeDeviceSessions(ctx context.Context, tx unitofwork.Transaction, userID string) (unitofwork.Mutation, error)
	readVerificationMailbox(ctx context.Context, tx unitofwork.Transaction, userID string, digest []byte, now int64, lastSeenAfter int64, email *string) error
	saveMailboxVerification(ctx context.Context, tx unitofwork.Transaction, userID string, email string, hash []byte, expiresAt int64) (unitofwork.Mutation, error)
	consumeMailboxVerification(ctx context.Context, tx unitofwork.Transaction, hash []byte, now int64, user *string, email *string) error
	saveVerifiedMailbox(ctx context.Context, tx unitofwork.Transaction, user string, email string, now int64) (unitofwork.Mutation, error)
	readVerifiedRecoveryAccount(ctx context.Context, tx unitofwork.Transaction, email string, user *string) error
	saveMailRecovery(ctx context.Context, tx unitofwork.Transaction, user string, email string, hash []byte, expiresAt int64) (unitofwork.Mutation, error)
	readInitialized(ctx context.Context, tx unitofwork.Transaction, initialized *bool) error
	claimInstallation(ctx context.Context, tx unitofwork.Transaction) (unitofwork.Mutation, error)
	insertOwnerAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email *string, hash string, username string) (unitofwork.Mutation, error)
	insertOwnerHousehold(ctx context.Context, tx unitofwork.Transaction, householdID string, householdName string, timezone string) (unitofwork.Mutation, error)
	insertOwnerMembership(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string) (unitofwork.Mutation, error)
	insertOwnerCapability(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, capability string) (unitofwork.Mutation, error)
	readLoginCredentials(ctx context.Context, tx unitofwork.Transaction, email string, email2 string, userID *string, storedHash *string, account *string) error
	readCurrentLoginMembership(ctx context.Context, tx unitofwork.Transaction, userID string, storedHash string, householdID *string) error
	clearLoginAccountAttempts(ctx context.Context, tx unitofwork.Transaction, accountKey []byte) (unitofwork.Mutation, error)
	deleteLoginSession(ctx context.Context, tx unitofwork.Transaction, digest []byte) (unitofwork.Mutation, error)
	expireLoginAttempts(ctx context.Context, tx unitofwork.Transaction, window int64) (unitofwork.Mutation, error)
	ensureAttemptCounter(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64) (unitofwork.Mutation, error)
	incrementAttemptCounter(ctx context.Context, tx unitofwork.Transaction, digest []byte, max int) (unitofwork.Mutation, error)
	touchSession(ctx context.Context, tx unitofwork.Transaction, now int64, digest []byte) (unitofwork.Mutation, error)
	rotateLoginSession(ctx context.Context, tx unitofwork.Transaction, old []byte) (unitofwork.Mutation, error)
	expireLoginSessions(ctx context.Context, tx unitofwork.Transaction, now int64, lastSeenBefore int64) (unitofwork.Mutation, error)
	insertLoginSession(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, householdID string, expiresAt int64, now int64) (unitofwork.Mutation, error)
	readInstallationOccupied(ctx context.Context, tx unitofwork.Transaction, occupied *bool) error
	restoreInstallation(ctx context.Context, tx unitofwork.Transaction) (unitofwork.Mutation, error)
	restoreHousehold(ctx context.Context, tx unitofwork.Transaction, householdID string, name string, timezone string) (unitofwork.Mutation, error)
	restoreAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email *string, passwordHash string, username string, assisted bool) (unitofwork.Mutation, error)
	restoreMembership(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, role string) (unitofwork.Mutation, error)
	restoreCapability(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, permission string) (unitofwork.Mutation, error)
	isExistingHouseholdMember(ctx context.Context, tx unitofwork.Transaction, householdID string, email string, exists *bool) error
	hasProtectedAdminInvitation(ctx context.Context, tx unitofwork.Transaction, householdID string, email string, now int64, protected *bool) error
	revokePreviousInvitations(ctx context.Context, tx unitofwork.Transaction, now int64, householdID string, email string) (unitofwork.Mutation, error)
	insertInvitation(ctx context.Context, tx unitofwork.Transaction, id string, householdID string, email string, role string, encoded string, digest []byte, issuerID string, membershipID string, now int64, expiresAt int64) (unitofwork.Mutation, error)
	readInvitationForRevocation(ctx context.Context, tx unitofwork.Transaction, target string, householdID string) (invitation, error)
	revokeInvitationRecord(ctx context.Context, tx unitofwork.Transaction, now int64, id string, householdID string) (unitofwork.Mutation, error)
	readInvitationByDigest(ctx context.Context, tx unitofwork.Transaction, digest []byte) (invitation, error)
	invitationAccountExists(ctx context.Context, tx unitofwork.Transaction, email string, exists *bool) error
	readInvitedAccount(ctx context.Context, tx unitofwork.Transaction, email string, userID *string, stored *string) error
	invitedAccountUnchanged(ctx context.Context, tx unitofwork.Transaction, userID string, stored string, unchanged *bool) error
	isInvitedSessionActive(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, now int64, lastSeenAfter int64, active *bool) error
	isInvitedMailboxTaken(ctx context.Context, tx unitofwork.Transaction, email string, exists *bool) error
	insertInvitedAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email string, stored string) (unitofwork.Mutation, error)
	hasInvitedMembership(ctx context.Context, tx unitofwork.Transaction, household string, userID string, exists *bool) error
	insertInvitedMembership(ctx context.Context, tx unitofwork.Transaction, userID string, household string, role string) (unitofwork.Mutation, error)
	claimInvitation(ctx context.Context, tx unitofwork.Transaction, now int64, id string, validAfter int64) (unitofwork.Mutation, error)
	clearMemberCapabilities(ctx context.Context, tx unitofwork.Transaction, household string, user string) (unitofwork.Mutation, error)
	insertMemberCapability(ctx context.Context, tx unitofwork.Transaction, user string, household string, cap string) (unitofwork.Mutation, error)
	deleteMembership(ctx context.Context, tx unitofwork.Transaction, householdID string, userID string) (unitofwork.Mutation, error)
	saveMembershipRole(ctx context.Context, tx unitofwork.Transaction, role string, householdID string, userID string) (unitofwork.Mutation, error)
	revokeMemberInvitations(ctx context.Context, tx unitofwork.Transaction, now int64, householdID string, userID string, email string) (unitofwork.Mutation, error)
	readSetupState(ctx context.Context, tx unitofwork.Transaction, ready *bool) error
	readLocalRecoveryAccount(ctx context.Context, tx unitofwork.Transaction, email string, email2 string, userID *string) error
	saveLocalRecovery(ctx context.Context, tx unitofwork.Transaction, userID string, digest []byte, expiresAt int64) (unitofwork.Mutation, error)
	isRecoveryAvailable(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, digest2 []byte, now2 int64, exists *bool) error
	consumeLocalRecovery(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, userID *string) error
	consumeMailRecovery(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, digest2 []byte, userID *string) error
	replaceRecoveredPassword(ctx context.Context, tx unitofwork.Transaction, hash string, userID string, email *string) error
	clearRecoveredAccountAttempts(ctx context.Context, tx unitofwork.Transaction, accountKey []byte) (unitofwork.Mutation, error)
	readHouseholdSettings(ctx context.Context, tx unitofwork.Transaction, householdID string, id *string, name *string, timezone *string) error
	saveHouseholdSettings(ctx context.Context, tx unitofwork.Transaction, name string, timezone string, householdID string) (unitofwork.Mutation, error)
	readMember(context.Context, unitofwork.Transaction, string, string) (access.Member, error)
	readSessionRecord(context.Context, []byte, int64) (*Session, int64, error)
	readBackupMemberRecords(context.Context, unitofwork.Transaction, string, int) ([]BackupMember, error)
	readInvitations(context.Context, string) ([]invitation, error)
	readMemberIDs(context.Context, string) ([]string, error)
	readPINDevices(context.Context, string, string) ([]map[string]any, error)
}
