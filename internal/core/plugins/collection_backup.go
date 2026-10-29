package plugins

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"os"
	"strings"
)

// CollectionBackup pins newly exported snapshots to the reviewed package.
// Historical built-in snapshots remain readable through an explicit v1 adapter.
type CollectionBackup struct {
	backup.Provider
	Package *Package
}

func (b CollectionBackup) Version() int { return 2 }
func (b CollectionBackup) table() string {
	return "plugin_" + strings.ReplaceAll(b.ID(), "-", "_") + "_collection_package"
}
func (b CollectionBackup) Tables() []string {
	return append(b.Provider.Tables(), "CREATE TABLE "+b.table()+" (sha256 TEXT NOT NULL)")
}
func (b CollectionBackup) Export(ctx context.Context, q backup.Queryer, tx *sql.Tx, household string, recoverable bool) error {
	if err := b.Provider.Export(ctx, q, tx, household, recoverable); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO "+b.table()+" VALUES(?)", b.Package.Digest)
	return err
}
func (b CollectionBackup) check(ctx context.Context, q backup.Queryer) error {
	var digest string
	var count int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM "+b.table()).Scan(&count); err != nil || count != 1 {
		return backup.Invalid
	}
	if err := q.QueryRowContext(ctx, "SELECT sha256 FROM "+b.table()).Scan(&digest); err != nil || digest != b.Package.Digest {
		return backup.ModuleRequired
	}
	return nil
}
func (b CollectionBackup) Inspect(ctx context.Context, q backup.Queryer, household string) (backup.Contents, error) {
	if err := b.check(ctx, q); err != nil {
		return backup.Contents{}, err
	}
	return b.Provider.Inspect(ctx, q, household)
}
func (b CollectionBackup) Prepare(ctx context.Context, q backup.Queryer, dir, household string) (backup.Plan, error) {
	if err := b.check(ctx, q); err != nil {
		return nil, err
	}
	return b.Provider.Prepare(ctx, q, dir, household)
}
func (b CollectionBackup) ProviderForVersion(version int) (backup.Provider, error) {
	if version == 2 {
		return b, nil
	}
	if version == 1 && b.Package.Manifest.Collection.LegacySnapshot {
		return b.Provider, nil
	}
	return nil, fmt.Errorf("unsupported collection snapshot version")
}

func (b CollectionBackup) OpenFileContext(ctx context.Context, key string) (*os.File, func(), error) {
	return backup.OpenOriginal(ctx, b.Provider, key)
}
