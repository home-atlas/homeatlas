package app

import (
	"context"
	"database/sql"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"golang.org/x/sync/semaphore"
)

func newBackups(ctx context.Context, db *sql.DB, accounts *identity.Service, dataDir string, participants ...backup.Participant) (*householdBackups, error) {
	if err := database.Apply(ctx, db, "app.household-backups", []database.Migration{{Version: 1, SQL: `INSERT OR IGNORE INTO identity_permissions(user_id,household_id,capability) SELECT user_id,household_id,'household.backup' FROM identity_memberships WHERE role='owner';`}}); err != nil {
		return nil, err
	}
	b := &householdBackups{store: &backupRepository{db: db}, accounts: accounts, dataDir: dataDir, participants: participants, resources: semaphore.NewWeighted(backupLockCapacity), mutations: semaphore.NewWeighted(1)}
	if err := b.validateParticipants(); err != nil {
		return nil, err
	}
	if err := b.prepareSchedules(ctx); err != nil {
		return nil, err
	}
	return b, nil
}
