// Package modules composes the module registry with HTTP and SQLite infrastructure.
package modules

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/home-atlas/homeatlas/internal/core/database"
	httpadapter "github.com/home-atlas/homeatlas/internal/core/modules/adapters/http"
	"github.com/home-atlas/homeatlas/internal/core/modules/adapters/sqlite"
	"github.com/home-atlas/homeatlas/internal/core/modules/application"
	"github.com/home-atlas/homeatlas/internal/core/modules/domain"
)

type Module interface {
	application.Module
	Migrations() []database.Migration
	Routes() []Route
}
type Registry struct {
	*application.Registry
	transport *httpadapter.Transport
	db        *sql.DB
}

func New(ctx context.Context, db *sql.DB) (*Registry, error) {
	if err := sqlite.Migrate(ctx, db); err != nil {
		return nil, err
	}
	registry := application.New(sqlite.New(db))
	return &Registry{Registry: registry, transport: httpadapter.New(registry), db: db}, nil
}
func (r *Registry) Register(ctx context.Context, module Module) error {
	err := r.Registry.Register(ctx, module, func(ctx context.Context) error {
		m := module.Manifest()
		if err := httpadapter.ValidateRoutes(m.ID, m.Capabilities, module.Routes()); err != nil {
			return err
		}
		return database.Apply(ctx, r.db, m.ID, module.Migrations())
	})
	if err == nil {
		r.transport.Register(module.Manifest().ID, module.Routes())
	}
	return err
}
func (r *Registry) Mount(mux *http.ServeMux) { r.transport.Mount(mux) }
func ValidID(id string) bool                 { return domain.ValidID(id) }
func Authorize(w http.ResponseWriter, r *http.Request, permission string) bool {
	return httpadapter.Authorize(w, r, permission)
}
func JSON(w http.ResponseWriter, status int, value any) { httpadapter.JSON(w, status, value) }

type Manifest = domain.Manifest
type Navigation = domain.Navigation
type SearchResult = domain.SearchResult
type Status = domain.Status
type Route = httpadapter.Route

const ContractVersion = domain.ContractVersion

var (
	ErrUnknownModule     = domain.ErrUnknownModule
	ErrUnavailable       = domain.ErrUnavailable
	ErrDependency        = domain.ErrDependency
	ErrHouseholdRequired = domain.ErrHouseholdRequired
)
