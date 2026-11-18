package application

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

// Repository composes the identity capabilities required by the application.
// Every operation in a transaction receives the same committing scope.
type Repository interface {
	AccountsRepository
	AuthenticationRepository
	AlternativeAccessRepository
	InvitationsRepository
	MembershipsRepository
	RecoveryRepository
	HouseholdRepository
	OwnAccountRepository
}

type OwnAccountRepository interface {
	ReadOwnAccount(context.Context, unitofwork.Transaction, []byte, string, string, string, int64, int64) (domain.AccountProfile, error)
	SaveOwnName(context.Context, unitofwork.Transaction, string, string) error
	ReadOwnSessions(context.Context, unitofwork.Transaction, string, string, []byte, int64, int64) ([]domain.AccountSession, error)
	DeleteOwnSession(context.Context, unitofwork.Transaction, string, string, []byte) error
	DeleteOtherOwnSessions(context.Context, unitofwork.Transaction, string, string, []byte) error
}

type AccountsRepository interface {
	Begin(context.Context) (unitofwork.Transaction, error)
	ReadInitialized(ctx context.Context, tx unitofwork.Transaction, initialized *bool) error
	ClaimInstallation(ctx context.Context, tx unitofwork.Transaction) (unitofwork.Mutation, error)
	InsertOwnerAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email *string, hash string, username string) (unitofwork.Mutation, error)
	InsertOwnerHousehold(ctx context.Context, tx unitofwork.Transaction, householdID string, HouseholdName string, timezone string) (unitofwork.Mutation, error)
	ReadInstallationOccupied(ctx context.Context, tx unitofwork.Transaction, occupied *bool) error
	RestoreInstallation(ctx context.Context, tx unitofwork.Transaction) (unitofwork.Mutation, error)
	RestoreHousehold(ctx context.Context, tx unitofwork.Transaction, householdID string, name string, timezone string) (unitofwork.Mutation, error)
	RestoreAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email *string, passwordHash string, username string, assisted bool) (unitofwork.Mutation, error)
}

type AuthenticationRepository interface {
	ReadLoginCredentials(ctx context.Context, tx unitofwork.Transaction, email string, email2 string, userID *string, storedHash *string, account *string) error
	ClearLoginAccountAttempts(ctx context.Context, tx unitofwork.Transaction, accountKey []byte) (unitofwork.Mutation, error)
	DeleteLoginSession(ctx context.Context, tx unitofwork.Transaction, digest []byte) (unitofwork.Mutation, error)
	ExpireLoginAttempts(ctx context.Context, tx unitofwork.Transaction, window int64) (unitofwork.Mutation, error)
	EnsureAttemptCounter(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64) (unitofwork.Mutation, error)
	IncrementAttemptCounter(ctx context.Context, tx unitofwork.Transaction, digest []byte, max int) (unitofwork.Mutation, error)
	TouchSession(ctx context.Context, tx unitofwork.Transaction, now int64, digest []byte) (unitofwork.Mutation, error)
	RotateLoginSession(ctx context.Context, tx unitofwork.Transaction, old []byte) (unitofwork.Mutation, error)
	ExpireLoginSessions(ctx context.Context, tx unitofwork.Transaction, now int64, lastSeenBefore int64) (unitofwork.Mutation, error)
	InsertLoginSession(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, householdID string, expiresAt int64, now int64) (unitofwork.Mutation, error)
	ReadSessionRecord(context.Context, []byte, int64) (*Session, int64, error)
}

type AlternativeAccessRepository interface {
	RevokeAssistedCredentials(context.Context, unitofwork.Transaction, string) error
	ReadPasswordProof(ctx context.Context, tx unitofwork.Transaction, userID string, stored *string) error
	IsPasswordProofCurrent(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, householdID string, now int64, lastSeenAfter int64, stored string, membershipID string, valid *bool) error
	AssistedAccountExists(ctx context.Context, tx unitofwork.Transaction, username string, email *string, exists *bool) error
	InsertAssistedAccount(ctx context.Context, tx unitofwork.Transaction, user string, name string, email *string, username string) (unitofwork.Mutation, error)
	InsertAssistedMembership(ctx context.Context, tx unitofwork.Transaction, user string, householdID string, role string) (unitofwork.Mutation, error)
	SaveAssistedEnrollment(ctx context.Context, tx unitofwork.Transaction, user string, householdID string, digest []byte, issuerID string, membershipID string, expiresAt int64) (unitofwork.Mutation, error)
	RecordEnrollmentIssued(ctx context.Context, tx unitofwork.Transaction, id string, user string, householdID string, actorID string, now int64) (unitofwork.Mutation, error)
	IsAssistedAccount(ctx context.Context, tx unitofwork.Transaction, userID string, assisted *bool) error
	ReadEnrollment(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, user *string, HouseholdID *string, name *string, username *string, IssuerID *string, IssuerMemberID *string) error
	RecordDeviceActivated(ctx context.Context, tx unitofwork.Transaction, id string, user string, HouseholdID string, IssuerID string, now int64) (unitofwork.Mutation, error)
	DeletePreviousPINBinding(ctx context.Context, tx unitofwork.Transaction, digest []byte) (unitofwork.Mutation, error)
	CountPINDevices(ctx context.Context, tx unitofwork.Transaction, user string, count *int) error
	InsertDeviceBinding(ctx context.Context, tx unitofwork.Transaction, id string, user string, HouseholdID string, digest []byte, hash string, name string, createdAt int64, expiresAt int64) (unitofwork.Mutation, error)
	ReadDeviceBinding(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, maximumFailures int, id *string, user *string, HouseholdID *string, hash *string, name *string, username *string, passwordHash *string) error
	RecordPINFailure(ctx context.Context, tx unitofwork.Transaction, id string) (unitofwork.Mutation, error)
	MarkPINSession(ctx context.Context, tx unitofwork.Transaction, digest []byte) (unitofwork.Mutation, error)
	ResetPINFailures(ctx context.Context, tx unitofwork.Transaction, id string) (unitofwork.Mutation, error)
	ReadAccountAccess(ctx context.Context, tx unitofwork.Transaction, userID string, username *string, hasPassword *bool) error
	SaveUsername(ctx context.Context, tx unitofwork.Transaction, username string, userID string) (unitofwork.Mutation, error)
	DeleteDeviceBinding(ctx context.Context, tx unitofwork.Transaction, target string, userID string, householdID string) (unitofwork.Mutation, error)
	RevokeDeviceSessions(ctx context.Context, tx unitofwork.Transaction, userID string) (unitofwork.Mutation, error)
	ReadPINDevices(context.Context, string, string) ([]map[string]any, error)
}

type InvitationsRepository interface {
	HasProtectedAdminInvitation(ctx context.Context, tx unitofwork.Transaction, householdID string, email string, now int64, protected *bool) error
	RevokePreviousInvitations(ctx context.Context, tx unitofwork.Transaction, now int64, householdID string, email string) (unitofwork.Mutation, error)
	InsertInvitation(ctx context.Context, tx unitofwork.Transaction, id string, householdID string, email string, role string, encoded string, digest []byte, issuerID string, membershipID string, now int64, expiresAt int64) (unitofwork.Mutation, error)
	ReadInvitationForRevocation(ctx context.Context, tx unitofwork.Transaction, target string, householdID string) (Invitation, error)
	RevokeInvitationRecord(ctx context.Context, tx unitofwork.Transaction, now int64, id string, householdID string) (unitofwork.Mutation, error)
	ReadInvitationByDigest(ctx context.Context, tx unitofwork.Transaction, digest []byte) (Invitation, error)
	InvitationAccountExists(ctx context.Context, tx unitofwork.Transaction, email string, exists *bool) error
	ReadInvitedAccount(ctx context.Context, tx unitofwork.Transaction, email string, userID *string, stored *string) error
	InvitedAccountUnchanged(ctx context.Context, tx unitofwork.Transaction, userID string, stored string, unchanged *bool) error
	IsInvitedSessionActive(ctx context.Context, tx unitofwork.Transaction, digest []byte, userID string, now int64, lastSeenAfter int64, active *bool) error
	IsInvitedMailboxTaken(ctx context.Context, tx unitofwork.Transaction, email string, exists *bool) error
	InsertInvitedAccount(ctx context.Context, tx unitofwork.Transaction, userID string, name string, email string, stored string) (unitofwork.Mutation, error)
	HasInvitedMembership(ctx context.Context, tx unitofwork.Transaction, HouseholdID string, userID string, exists *bool) error
	InsertInvitedMembership(ctx context.Context, tx unitofwork.Transaction, userID string, HouseholdID string, role string) (unitofwork.Mutation, error)
	ClaimInvitation(ctx context.Context, tx unitofwork.Transaction, now int64, id string, validAfter int64) (unitofwork.Mutation, error)
	RevokeMemberInvitations(ctx context.Context, tx unitofwork.Transaction, now int64, householdID string, userID string, email string) (unitofwork.Mutation, error)
	ReadInvitations(context.Context, string) ([]Invitation, error)
}

type MembershipsRepository interface {
	InsertOwnerMembership(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string) (unitofwork.Mutation, error)
	InsertOwnerCapability(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, capability string) (unitofwork.Mutation, error)
	ReadCurrentLoginMembership(ctx context.Context, tx unitofwork.Transaction, userID string, storedHash string, householdID *string) error
	RestoreMembership(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, role string) (unitofwork.Mutation, error)
	RestoreCapability(ctx context.Context, tx unitofwork.Transaction, userID string, householdID string, permission string) (unitofwork.Mutation, error)
	IsExistingHouseholdMember(ctx context.Context, tx unitofwork.Transaction, householdID string, email string, exists *bool) error
	ClearMemberCapabilities(ctx context.Context, tx unitofwork.Transaction, HouseholdID string, user string) (unitofwork.Mutation, error)
	InsertMemberCapability(ctx context.Context, tx unitofwork.Transaction, user string, HouseholdID string, cap string) (unitofwork.Mutation, error)
	DeleteMembership(ctx context.Context, tx unitofwork.Transaction, householdID string, userID string) (unitofwork.Mutation, error)
	SaveMembershipRole(ctx context.Context, tx unitofwork.Transaction, role string, householdID string, userID string) (unitofwork.Mutation, error)
	ReadMember(context.Context, unitofwork.Transaction, string, string) (access.Member, error)
	ReadBackupMemberRecords(context.Context, unitofwork.Transaction, string, int) ([]BackupMember, error)
	ReadMemberIDs(context.Context, string) ([]string, error)
}

type RecoveryRepository interface {
	RevokeRecoveredCredentials(context.Context, unitofwork.Transaction, string, int64) error
	ReadVerificationMailbox(ctx context.Context, tx unitofwork.Transaction, userID string, digest []byte, now int64, lastSeenAfter int64, email *string) error
	SaveMailboxVerification(ctx context.Context, tx unitofwork.Transaction, userID string, email string, hash []byte, expiresAt int64) (unitofwork.Mutation, error)
	ConsumeMailboxVerification(ctx context.Context, tx unitofwork.Transaction, hash []byte, now int64, user *string, email *string) error
	SaveVerifiedMailbox(ctx context.Context, tx unitofwork.Transaction, user string, email string, now int64) (unitofwork.Mutation, error)
	ReadVerifiedRecoveryAccount(ctx context.Context, tx unitofwork.Transaction, email string, user *string) error
	SaveMailRecovery(ctx context.Context, tx unitofwork.Transaction, user string, email string, hash []byte, expiresAt int64) (unitofwork.Mutation, error)
	ReadSetupState(ctx context.Context, tx unitofwork.Transaction, ready *bool) error
	ReadLocalRecoveryAccount(ctx context.Context, tx unitofwork.Transaction, email string, email2 string, userID *string) error
	SaveLocalRecovery(ctx context.Context, tx unitofwork.Transaction, userID string, digest []byte, expiresAt int64) (unitofwork.Mutation, error)
	IsRecoveryAvailable(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, digest2 []byte, now2 int64, exists *bool) error
	ConsumeLocalRecovery(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, userID *string) error
	ConsumeMailRecovery(ctx context.Context, tx unitofwork.Transaction, digest []byte, now int64, digest2 []byte, userID *string) error
	ReplaceRecoveredPassword(ctx context.Context, tx unitofwork.Transaction, hash string, userID string, email *string) error
	ClearRecoveredAccountAttempts(ctx context.Context, tx unitofwork.Transaction, accountKey []byte) (unitofwork.Mutation, error)
}

type HouseholdRepository interface {
	ReadHouseholdSettings(ctx context.Context, tx unitofwork.Transaction, householdID string, id *string, name *string, timezone *string) error
	SaveHouseholdSettings(ctx context.Context, tx unitofwork.Transaction, name string, timezone string, householdID string) (unitofwork.Mutation, error)
}
