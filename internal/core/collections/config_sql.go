package collections

import (
	"context"
	"database/sql"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

func (c Config) sql(query string) string {
	id := strings.ReplaceAll(c.ID, "-", "_")
	if !c.LegacySnapshot {
		id = "plugin_collection_" + id
	}
	grants := id + "_shares"
	if c.LegacySnapshot {
		grants = strings.TrimSuffix(id, "s") + "_shares"
	}
	snapshotShares := id + "_backup_shares"
	if c.LegacySnapshot {
		snapshotShares = "shares"
	}
	if c.LegacySnapshot {
		query = strings.ReplaceAll(query, "item_id", strings.TrimSuffix(id, "s")+"_id")
	}
	rewritten := strings.NewReplacer("collection_snapshot_grants", snapshotShares, "collection_items", id, "collection_grants", grants, "collection_fts", id+"_fts", "collection_access", id+"_access", "collection_file_key", id+"_file_key", "collection_ai", id+"_ai", "collection_ad", id+"_ad", "collection_au", id+"_au").Replace(query)
	return strings.ReplaceAll(rewritten, "'__collection_default__'", "'"+c.Categories[len(c.Categories)-1].Value+"'")
}

type scopedDB struct {
	*sql.DB
	config Config
}

func (d *scopedDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, d.config.sql(q), args...)
}
func (d *scopedDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, d.config.sql(q), args...)
}
func (d *scopedDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*scopedTx, error) {
	tx, err := d.DB.BeginTx(ctx, opts)
	return &scopedTx{tx, d.config}, err
}

type scopedTx struct {
	*sql.Tx
	config Config
}

func (t *scopedTx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(ctx, t.config.sql(q), args...)
}
func (t *scopedTx) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(ctx, t.config.sql(q), args...)
}
func (t *scopedTx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(ctx, t.config.sql(q), args...)
}

type scopedQuery struct {
	backup.Queryer
	config Config
}

func (q scopedQuery) QueryContext(ctx context.Context, s string, args ...any) (*sql.Rows, error) {
	return q.Queryer.QueryContext(ctx, q.config.sql(s), args...)
}
func (q scopedQuery) QueryRowContext(ctx context.Context, s string, args ...any) *sql.Row {
	return q.Queryer.QueryRowContext(ctx, q.config.sql(s), args...)
}

func StorageCollection(config Config, directory string) objectstore.Collection {
	return objectstore.Collection{ID: config.ID, Table: config.sql("collection_items"), Directory: directory}
}

func (t *scopedTx) SQLTransaction() *sql.Tx { return t.Tx }
