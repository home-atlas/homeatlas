package sqlite

import (
	"database/sql"

	"github.com/home-atlas/homeatlas/internal/core/identity/application"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
)

func New(db *sql.DB) *Repository { return &Repository{db: db} }

var _ application.Repository = (*Repository)(nil)

type Session = domain.Session
type BackupMember = domain.BackupMember
type Invitation = domain.Invitation
