package modules

import (
	"context"
	"database/sql"
	"errors"

	"github.com/home-atlas/homeatlas/internal/core/access"
)

var (
	ErrUnknownModule     = errors.New("unknown module")
	ErrUnavailable       = errors.New("module unavailable in installation")
	ErrDependency        = errors.New("module dependency conflict")
	ErrHouseholdRequired = errors.New("authenticated household required")
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// The installation state is an upper bound; household preferences never enable a globally blocked module.
func (r *Registry) effective(ctx context.Context, q queryer, household string) ([]Status, error) {
	rows, err := q.QueryContext(ctx, `SELECT module_id,enabled FROM household_module_states WHERE household_id=?`, household)
	if err != nil {
		return nil, err
	}
	overrides := map[string]bool{}
	for rows.Next() {
		var id string
		var enabled bool
		if err := rows.Scan(&id, &enabled); err != nil {
			rows.Close()
			return nil, err
		}
		overrides[id] = enabled
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	states := make([]Status, 0, len(r.order))
	enabled := map[string]bool{}
	// Registration is topological: dependencies always precede their consumers.
	for _, id := range r.order {
		e := r.entries[id]
		active := e.enabled
		if override, exists := overrides[id]; exists {
			active = active && override
		}
		for _, dependency := range e.module.Manifest().Dependencies {
			active = active && enabled[dependency]
		}
		enabled[id] = active
		states = append(states, Status{Manifest: e.module.Manifest(), Enabled: active, Available: e.enabled, AccessPermission: e.module.SearchPermission()})
	}
	return states, nil
}

func moduleEnabled(states []Status, id string) bool {
	for _, s := range states {
		if s.ID == id {
			return s.Enabled
		}
	}
	return false
}

func (r *Registry) ListFor(ctx context.Context, household string) ([]Status, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.effective(ctx, r.db, household)
}

// Authorize under the same transaction as the state change. Keep the lock→DB order used by routes/search.
func (r *Registry) SetHouseholdEnabled(ctx context.Context, id string, enabled bool, authorize func(*sql.Tx) error) error {
	p, ok := access.Current(ctx)
	if !ok || authorize == nil {
		return ErrHouseholdRequired
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := authorize(tx); err != nil {
		return err
	}
	e, exists := r.entries[id]
	if !exists {
		return ErrUnknownModule
	}
	if !e.enabled {
		return ErrUnavailable
	}
	states, err := r.effective(ctx, tx, p.HouseholdID)
	if err != nil {
		return err
	}
	if enabled {
		for _, dep := range e.module.Manifest().Dependencies {
			if !moduleEnabled(states, dep) {
				return ErrDependency
			}
		}
	} else {
		for _, other := range states {
			if !other.Enabled {
				continue
			}
			for _, dep := range other.Dependencies {
				if dep == id {
					return ErrDependency
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO household_module_states(household_id,module_id,enabled) VALUES(?,?,?)
 ON CONFLICT(household_id,module_id) DO UPDATE SET enabled=excluded.enabled`, p.HouseholdID, id, enabled); err != nil {
		return err
	}
	return tx.Commit()
}
