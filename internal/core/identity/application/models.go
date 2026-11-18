package application

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type User = domain.User
type Household = domain.Household
type Session = domain.Session
type State = domain.State
type BackupMember = domain.BackupMember
type Invitation = domain.Invitation

const Manage = domain.Manage

type ModuleControl interface {
	SetHouseholdEnabled(context.Context, string, bool, func(unitofwork.Transaction) error) error
}
