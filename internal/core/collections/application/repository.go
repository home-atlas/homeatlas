package application

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/access"
	modules "github.com/home-atlas/homeatlas/internal/core/modules/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository interface {
	Enabled(context.Context, unitofwork.Transaction, string) (bool, error)
	Publish(context.Context, access.Principal, Item, PendingOriginal, func(unitofwork.Transaction) error) error
	Begin(context.Context) (unitofwork.Transaction, error)
	List(context.Context, access.Principal, ListInput) ([]Item, error)
	Search(context.Context, access.Principal, string) ([]modules.SearchResult, error)
	Lookup(context.Context, access.Principal, string, bool) (Item, error)
	Grants(context.Context, string, string) ([]Recipient, error)
	Owns(context.Context, unitofwork.Transaction, access.Principal, string) (bool, error)
	ChangeGrant(context.Context, unitofwork.Transaction, access.Principal, string, string, string, bool) error
	Update(context.Context, unitofwork.Transaction, access.Principal, string, Metadata) (Item, error)
}
