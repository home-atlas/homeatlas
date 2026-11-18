package httpadapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/plugins/domain"
)

type Runner interface {
	Run(context.Context, *domain.Package, string, map[string]string) (domain.Result, error)
}
type Module struct {
	Package *Package
	Engine  Runner
}

func (m *Module) Manifest() modules.Manifest {
	p := m.Package.Manifest
	return modules.Manifest{ID: p.ID, Name: p.Name, Version: p.Version, ContractVersion: modules.ContractVersion, Runtime: "wasi-preview1", Capabilities: m.Package.Capabilities(), Dependencies: []string{}, Navigation: modules.Navigation{Label: p.Name, Path: "/" + p.ID}}
}
func (*Module) Migrations() []database.Migration { return nil }
func (m *Module) SearchPermission() string       { return m.Package.Manifest.ID + ".read" }
func (*Module) Search(context.Context, string) ([]modules.SearchResult, error) {
	return []modules.SearchResult{}, nil
}
func (m *Module) Routes() []modules.Route {
	routes := []modules.Route{
		{Method: "GET", Path: "/", Permission: m.SearchPermission(), Handler: func(w http.ResponseWriter, r *http.Request) { modules.JSON(w, 200, m.Package.Manifest) }},
	}
	for _, view := range m.Package.Manifest.Views {
		permission := m.Package.Manifest.ID + ".write"
		if view.ReadOnly {
			permission = m.SearchPermission()
		}
		if view.Permission == "share" {
			permission = m.Package.Manifest.ID + ".share"
		}
		routes = append(routes, modules.Route{Method: "POST", Path: "/actions/" + view.ID, Permission: permission, Handler: func(w http.ResponseWriter, r *http.Request) { m.run(w, r, view) }})
	}
	return routes
}
func (m *Module) run(w http.ResponseWriter, r *http.Request, view View) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	var input map[string]string
	if err == nil {
		err = domain.Decode(body, &input)
	}
	valid := err == nil && len(input) <= len(view.Fields)
	known := map[string]bool{}
	for _, f := range view.Fields {
		known[f.Name] = true
		value := input[f.Name]
		if len(value) > 2000 || (f.Required && !domain.Text(value, 2000)) {
			valid = false
		}
		if f.Type == "number" && value != "" {
			if n, err := strconv.ParseFloat(value, 64); err != nil || n != n || n > 1e15 || n < -1e15 {
				valid = false
			}
		}
	}
	for name := range input {
		if !known[name] {
			valid = false
		}
	}
	if !valid {
		modules.JSON(w, 400, map[string]string{"error": "invalid_plugin_input"})
		return
	}
	result, err := m.Engine.Run(r.Context(), m.Package, view.ID, input)
	if errors.Is(err, ErrStorageDenied) {
		modules.JSON(w, 403, map[string]string{"error": "permission_denied"})
		return
	}
	if errors.Is(err, ErrStorageConflict) {
		modules.JSON(w, 409, map[string]string{"error": "plugin_storage_changed"})
		return
	}
	if errors.Is(err, ErrBusy) {
		w.Header().Set("Retry-After", "1")
		modules.JSON(w, 429, map[string]string{"error": "plugin_busy"})
		return
	}
	if err != nil {
		modules.JSON(w, 502, map[string]string{"error": "plugin_failed"})
		return
	}
	modules.JSON(w, 200, result)
}

type Package = domain.Package
type Manifest = domain.Manifest
type View = domain.View
type Field = domain.Field
type StorageRequest = domain.StorageRequest
type StorageResponse = domain.StorageResponse
type SharedRecord = domain.SharedRecord
type SharedChange = domain.SharedChange
type GrantKey = domain.GrantKey
type SharedGrant = domain.SharedGrant
type Result = domain.Result

var (
	ErrStorageDenied   = domain.ErrStorageDenied
	ErrStorageConflict = domain.ErrStorageConflict
	ErrBusy            = domain.ErrBusy
)
