package application

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules/domain"
)

type Module interface {
	Manifest() domain.Manifest
	SearchPermission() string
	Search(context.Context, string) ([]domain.SearchResult, error)
}
type entry struct {
	module  Module
	enabled bool
}

type Registry struct {
	store   Repository
	mu      sync.RWMutex
	entries map[string]*entry
	order   []string
}

func New(store Repository) *Registry {
	return &Registry{store: store, entries: make(map[string]*entry)}
}
func (r *Registry) Register(ctx context.Context, module Module, initialize func(context.Context) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := module.Manifest()
	if !ValidID(m.ID) || m.Name == "" || m.Version == "" || m.ContractVersion != ContractVersion || m.Navigation.Path != "/"+m.ID {
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
	if initialize != nil {
		if err := initialize(ctx); err != nil {
			return err
		}
	}
	enabled, err := r.store.Register(ctx, m.ID)
	if err != nil {
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
	if err := r.store.SetEnabled(ctx, id, enabled); err != nil {
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

func (r *Registry) Search(ctx context.Context, query string) ([]SearchResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := []SearchResult{}
	p, ok := access.Current(ctx)
	if !ok {
		return result, nil
	}
	states, err := r.effective(ctx, nil, p.HouseholdID)
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

// WithEnabled retains the registry read lock while an authorized module operation runs.
func (r *Registry) WithEnabled(ctx context.Context, id string, invoke func()) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok || !e.enabled {
		return domain.ErrUnknownModule
	}
	p, ok := access.Current(ctx)
	if !ok {
		return domain.ErrHouseholdRequired
	}
	states, err := r.effective(ctx, nil, p.HouseholdID)
	if err != nil {
		return err
	}
	if !moduleEnabled(states, id) {
		return domain.ErrUnknownModule
	}
	invoke()
	return nil
}
