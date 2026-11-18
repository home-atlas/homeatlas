// Package identity composes identity policy with HTTP and SQLite adapters.
package identity

import (
	"context"
	"database/sql"
	"time"

	httpadapter "github.com/home-atlas/homeatlas/internal/core/identity/adapters/http"
	"github.com/home-atlas/homeatlas/internal/core/identity/adapters/sqlite"
	"github.com/home-atlas/homeatlas/internal/core/identity/application"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
)

type Service struct {
	*application.Application
	*httpadapter.Transport
	database *sql.DB
	config   Config
}

func New(ctx context.Context, db *sql.DB, config Config) (*Service, error) {
	if _, err := application.ParseOrigin(config.PublicURL); err != nil {
		return nil, err
	}
	if err := sqlite.Migrate(ctx, db); err != nil {
		return nil, err
	}
	app, err := application.New(ctx, sqlite.New(db), config)
	if err != nil {
		return nil, err
	}
	transport, err := httpadapter.New(app, config.PublicURL, config.Modules != nil)
	if err != nil {
		app.Close()
		return nil, err
	}
	return &Service{Application: app, Transport: transport, database: db, config: config}, nil
}
func IssueRecovery(ctx context.Context, db *sql.DB, email string) (string, time.Time, error) {
	return application.RecoveryFor(ctx, sqlite.New(db), email)
}
func ValidateBackupMembers(members []BackupMember) error {
	return application.ValidateBackupMembers(members)
}

type User = domain.User
type Household = domain.Household
type Session = domain.Session
type State = domain.State
type BackupMember = domain.BackupMember
type Config = application.Config
type Mailer = application.Mailer
type Client = application.Client
type Result = application.Result
type Application = application.Application

const Manage = domain.Manage

var (
	ErrInstallationNotEmpty = application.ErrInstallationNotEmpty
	ErrInvalidSetupCode     = application.ErrInvalidSetupCode
	ErrRecoveryMembers      = application.ErrRecoveryMembers
)
