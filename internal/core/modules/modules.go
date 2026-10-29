package modules

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
)

const ContractVersion = "1"

type Navigation struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type Manifest struct {
	Collection      json.RawMessage `json:"collection,omitempty"`
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Version         string          `json:"version"`
	ContractVersion string          `json:"contractVersion"`
	Runtime         string          `json:"runtime,omitempty"`
	Capabilities    []string        `json:"capabilities"`
	Dependencies    []string        `json:"dependencies"`
	Navigation      Navigation      `json:"navigation"`
}

type Route struct {
	Method     string
	Path       string
	Permission string
	Handler    http.HandlerFunc
}

type SearchResult struct {
	Module string `json:"module"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Path   string `json:"path"`
}

// Module is a trusted host-side contract. External guests use the constrained WASI adapter.
type Module interface {
	Manifest() Manifest
	Migrations() []database.Migration
	Routes() []Route
	SearchPermission() string
	Search(context.Context, string) ([]SearchResult, error)
}

type Status struct {
	Manifest
	Enabled          bool   `json:"enabled"`
	Available        bool   `json:"available"`
	AccessPermission string `json:"accessPermission"`
}

type entry struct {
	module  Module
	enabled bool
}

type Registry struct {
	db      *sql.DB
	mu      sync.RWMutex
	entries map[string]*entry
	order   []string
}

func New(ctx context.Context, db *sql.DB) (*Registry, error) {
	err := database.Apply(ctx, db, "core.modules", []database.Migration{{Version: 1, SQL: `CREATE TABLE module_states (id TEXT PRIMARY KEY, enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)))`}, {Version: 2, SQL: `
 CREATE TABLE household_module_states (
   household_id TEXT NOT NULL, module_id TEXT NOT NULL REFERENCES module_states(id),
   enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), PRIMARY KEY(household_id,module_id));
 `}})
	return &Registry{db: db, entries: make(map[string]*entry)}, err
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var method = regexp.MustCompile(`^[A-Z]+$`)
var reserved = map[string]bool{"modules": true, "health": true, "search": true, "auth": true, "setup": true, "household": true, "invitations": true, "settings": true, "family": true, "join": true, "login": true, "recover": true, "verify-email": true}

func (r *Registry) Register(ctx context.Context, module Module) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := module.Manifest()
	if !identifier.MatchString(m.ID) || reserved[m.ID] || m.Name == "" || m.Version == "" || m.ContractVersion != ContractVersion || m.Navigation.Path != "/"+m.ID {
		return fmt.Errorf("invalid manifest: %q", m.ID)
	}
	if _, exists := r.entries[m.ID]; exists {
		return fmt.Errorf("duplicate module: %s", m.ID)
	}
	for _, dependency := range m.Dependencies {
		if _, exists := r.entries[dependency]; !exists {
			return fmt.Errorf("%s: register dependency %s first", m.ID, dependency)
		}
	}
	declared := func(permission string) bool {
		for _, capability := range m.Capabilities {
			if capability == permission && strings.HasPrefix(capability, m.ID+".") {
				return true
			}
		}
		return false
	}
	if !declared(module.SearchPermission()) {
		return fmt.Errorf("%s: undeclared search capability", m.ID)
	}
	seen := make(map[string]bool)
	for _, route := range module.Routes() {
		key := route.Method + " " + route.Path
		if !declared(route.Permission) || route.Handler == nil || !method.MatchString(route.Method) || !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, " ?#\t\n\r") || seen[key] {
			return fmt.Errorf("%s: invalid route %q", m.ID, key)
		}
		seen[key] = true
	}
	if err := database.Apply(ctx, r.db, m.ID, module.Migrations()); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, "INSERT INTO module_states (id, enabled) VALUES (?, 1) ON CONFLICT(id) DO NOTHING", m.ID); err != nil {
		return err
	}
	var enabled bool
	if err := r.db.QueryRowContext(ctx, "SELECT enabled FROM module_states WHERE id = ?", m.ID).Scan(&enabled); err != nil {
		return err
	}
	for _, dependency := range m.Dependencies {
		if enabled && !r.entries[dependency].enabled {
			return fmt.Errorf("%s: dependency %s is disabled", m.ID, dependency)
		}
	}
	r.entries[m.ID] = &entry{module: module, enabled: enabled}
	r.order = append(r.order, m.ID)
	return nil
}

// SetEnabled is for trusted application code; it is deliberately not an unauthenticated API.
func (r *Registry) SetEnabled(ctx context.Context, id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return fmt.Errorf("unknown module: %s", id)
	}
	for otherID, other := range r.entries {
		for _, dependency := range other.module.Manifest().Dependencies {
			if enabled && otherID == id && !r.entries[dependency].enabled {
				return fmt.Errorf("dependency disabled: %s", dependency)
			}
			if !enabled && other.enabled && dependency == id {
				return fmt.Errorf("%s is required by %s", id, otherID)
			}
		}
	}
	if _, err := r.db.ExecContext(ctx, "UPDATE module_states SET enabled = ? WHERE id = ?", enabled, id); err != nil {
		return err
	}
	e.enabled = enabled
	return nil
}

func (r *Registry) List() []Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Status, 0, len(r.order))
	for _, id := range r.order {
		e := r.entries[id]
		result = append(result, Status{Manifest: e.module.Manifest(), Enabled: e.enabled, Available: e.enabled, AccessPermission: e.module.SearchPermission()})
	}
	return result
}

func (r *Registry) Mount(mux *http.ServeMux) {
	for _, id := range r.order {
		e := r.entries[id]
		for _, route := range e.module.Routes() {
			path := "/api/" + id + strings.TrimSuffix(route.Path, "/")
			mux.HandleFunc(route.Method+" "+path, func(w http.ResponseWriter, req *http.Request) {
				r.mu.RLock()
				defer r.mu.RUnlock()
				if !e.enabled {
					http.NotFound(w, req)
					return
				}
				p, ok := access.Current(req.Context())
				if !ok {
					Authorize(w, req, route.Permission)
					return
				}
				states, err := r.effective(req.Context(), r.db, p.HouseholdID)
				if err != nil {
					JSON(w, 503, map[string]string{"error": "modules_unavailable"})
					return
				}
				if !moduleEnabled(states, id) {
					http.NotFound(w, req)
					return
				}
				if !Authorize(w, req, route.Permission) {
					return
				}
				route.Handler(w, req)
			})
		}
	}
}

func (r *Registry) Search(ctx context.Context, query string) ([]SearchResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := []SearchResult{}
	p, ok := access.Current(ctx)
	if !ok {
		return result, nil
	}
	states, err := r.effective(ctx, r.db, p.HouseholdID)
	if err != nil {
		return nil, err
	}
	for _, id := range r.order {
		e := r.entries[id]
		if !moduleEnabled(states, id) || !access.Allows(ctx, e.module.SearchPermission()) {
			continue
		}
		matches, err := e.module.Search(ctx, query)
		if err != nil {
			return nil, err
		}
		result = append(result, matches...)
	}
	return result, nil
}

func Authorize(w http.ResponseWriter, req *http.Request, permission string) bool {
	if _, ok := access.Current(req.Context()); !ok {
		JSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication_required"})
		return false
	}
	if !access.Allows(req.Context(), permission) {
		JSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied"})
		return false
	}
	return true
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
