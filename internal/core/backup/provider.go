// Package backup defines the trusted built-in module backup contract.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
)

var Invalid = errors.New("invalid backup")
var Conflict = errors.New("backup household conflict")
var OriginalUnavailable = errors.New("backup original unavailable")
var ModuleRequired = errors.New("backup module unavailable or incompatible")

// Queryer is shared by the live snapshot transaction and the validated archive DB.
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type File struct {
	Key, SHA256 string
	Size        int64
}
type Contents struct {
	Files       []File
	Unavailable []string
	Records     int
}
type RestoreContext struct {
	HouseholdID string
	// Nil for existing-home restore; recovery maps account IDs to source memberships.
	RecoveryMemberships map[string]string
}

// Plan stages files before the transaction. Apply mutates only through the shared tx.
// Finish must remove staged files after rollback, or obsolete files after commit.
// keepOld protects damaged originals retained by a degraded pre-restore copy.
type Plan interface {
	Apply(context.Context, *sql.Tx, RestoreContext) error
	Finish(committed, keepOld bool)
}

// Provider owns its snapshot schemas, validation and domain authorization. Tables
// are static trusted code, never SQL supplied by an archive. Export writes only
// to the isolated snapshot. Present must reject incomplete live schemas.
type Provider interface {
	ID() string
	Version() int
	MaxFileBytes() int64
	Tables() []string
	Present(context.Context, Queryer) (bool, error)
	Empty(context.Context, Queryer) (bool, error)
	Export(context.Context, Queryer, *sql.Tx, string, bool) error
	Inspect(context.Context, Queryer, string) (Contents, error)
	OpenFile(string) (*os.File, error)
	Prepare(context.Context, Queryer, string, string) (Plan, error)
}

// Participant is installation policy, including the v1-v3 compatibility adapter.
// Future providers use the generic v4 envelope without changing the coordinator.
type Participant struct {
	Provider
	LegacyPrefix     string
	LegacyCountField string
}
