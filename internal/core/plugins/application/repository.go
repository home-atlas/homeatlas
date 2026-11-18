package application

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository interface {
	Begin(ctx context.Context) (unitofwork.Transaction, error)
	Allowed(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal, write bool) (bool, error)
	Revision(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal) (int64, string, error)
	Keys(ctx context.Context, plugin string, p access.Principal, After string, limit int) (map[string]bool, error)
	Value(ctx context.Context, plugin string, p access.Principal, key string) (string, error)
	WritePrivate(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal, pending map[string]*string) error
	SharedRevision(ctx context.Context, tx unitofwork.Transaction, plugin, home string) (int64, error)
	HasCapability(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal, cap string) (bool, error)
	SharedRecord(ctx context.Context, tx unitofwork.Transaction, plugin, home, key string) (*SharedRecord, error)
	Grant(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal, id string) (string, error)
	Recipient(ctx context.Context, tx unitofwork.Transaction, plugin, home, User, permission string) (string, error)
	SharedKeys(ctx context.Context, plugin string, p access.Principal, After string, take int) (map[string]bool, error)
	Grants(ctx context.Context, plugin, home, id, After string, limit int) (map[string]SharedGrant, error)
	SharedByID(ctx context.Context, tx unitofwork.Transaction, plugin, home, id string) (*SharedRecord, error)
	WriteShared(ctx context.Context, scope unitofwork.Transaction, plugin string, p access.Principal, pending map[string]SharedChange, Grants map[GrantKey]*SharedGrant) error
}
