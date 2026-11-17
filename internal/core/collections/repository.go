package collections

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository interface {
	enabled(context.Context, unitofwork.Transaction, string) (bool, error)
	publish(context.Context, access.Principal, Item, *objectstore.Pending, func(unitofwork.Transaction) error) error
	Begin(context.Context) (unitofwork.Transaction, error)
	list(context.Context, access.Principal, ListInput) ([]Item, error)
	search(context.Context, access.Principal, string) ([]modules.SearchResult, error)
	lookup(context.Context, access.Principal, string, bool) (Item, error)
	grants(context.Context, string, string) ([]recipient, error)
	owns(context.Context, unitofwork.Transaction, access.Principal, string) (bool, error)
	changeGrant(context.Context, unitofwork.Transaction, access.Principal, string, string, string, bool) error
	update(context.Context, unitofwork.Transaction, access.Principal, string, metadata) (Item, error)
}
