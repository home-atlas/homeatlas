package sqlite

import (
	"context"
	"database/sql"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository struct{ db *sql.DB }

func (s *Repository) Begin(ctx context.Context) (unitofwork.Transaction, error) {
	return s.db.BeginTx(ctx, nil)
}
func (s *Repository) Register(ctx context.Context, id string) (bool, error) {
	if _, err := s.db.ExecContext(ctx, "INSERT INTO module_states (id, enabled) VALUES (?, 1) ON CONFLICT(id) DO NOTHING", id); err != nil {
		return false, err
	}
	var enabled bool
	err := s.db.QueryRowContext(ctx, "SELECT enabled FROM module_states WHERE id = ?", id).Scan(&enabled)
	return enabled, err
}
func (s *Repository) SetEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE module_states SET enabled = ? WHERE id = ?", enabled, id)
	return err
}
func (s *Repository) Overrides(ctx context.Context, tx unitofwork.Transaction, household string) (map[string]bool, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return nil, err
	}
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
	return overrides, nil
}
func (s *Repository) SetHouseholdEnabled(ctx context.Context, tx unitofwork.Transaction, home, id string, enabled bool) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO household_module_states(household_id,module_id,enabled) VALUES(?,?,?)
 ON CONFLICT(household_id,module_id) DO UPDATE SET enabled=excluded.enabled`, home, id, enabled)
	return err
}

func New(db *sql.DB) *Repository { return &Repository{db: db} }
func Migrate(ctx context.Context, db *sql.DB) error {
	return database.Apply(ctx, db, "core.modules", []database.Migration{{Version: 1, SQL: `CREATE TABLE module_states (id TEXT PRIMARY KEY, enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)))`}, {Version: 2, SQL: `
 CREATE TABLE household_module_states (
   household_id TEXT NOT NULL, module_id TEXT NOT NULL REFERENCES module_states(id),
   enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), PRIMARY KEY(household_id,module_id));
 `}})
}
