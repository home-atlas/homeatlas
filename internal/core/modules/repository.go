package modules

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository interface {
	Begin(context.Context) (unitofwork.Transaction, error)
	register(context.Context, string, []database.Migration) (bool, error)
	setEnabled(context.Context, string, bool) error
	overrides(context.Context, unitofwork.Transaction, string) (map[string]bool, error)
	setHouseholdEnabled(context.Context, unitofwork.Transaction, string, string, bool) error
}
