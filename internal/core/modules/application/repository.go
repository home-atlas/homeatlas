package application

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository interface {
	Begin(context.Context) (unitofwork.Transaction, error)
	Register(context.Context, string) (bool, error)
	SetEnabled(context.Context, string, bool) error
	Overrides(context.Context, unitofwork.Transaction, string) (map[string]bool, error)
	SetHouseholdEnabled(context.Context, unitofwork.Transaction, string, string, bool) error
}
