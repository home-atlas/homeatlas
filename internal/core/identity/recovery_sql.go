package identity

import (
	"context"
	"database/sql"
	"time"
)

func IssueRecovery(ctx context.Context, db *sql.DB, identifier string) (string, time.Time, error) {
	return (&Application{store: &sqliteRepository{db: db}}).issueRecovery(ctx, identifier)
}
