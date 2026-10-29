// Package collections implements the host-owned file/metadata/ACL primitives
// available to isolated plugins. Package rules run through Validate, never SQL.
package collections

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type Definition struct {
	TitleMaxLength       int      `json:"titleMaxLength"`
	DescriptionMaxLength int      `json:"descriptionMaxLength"`
	MaxTags              int      `json:"maxTags"`
	TagMaxLength         int      `json:"tagMaxLength"`
	Title                string   `json:"title"`
	Description          string   `json:"description"`
	Categories           []Option `json:"categories"`
	MediaTypes           []string `json:"mediaTypes"`
	MaxFileBytes         int64    `json:"maxFileBytes"`
	LegacySnapshot       bool     `json:"legacySnapshot,omitempty"`
}
type Config struct {
	Storage           *objectstore.Store
	ID, Name, Version string
	Definition
	Validate func(context.Context, []byte) ([]byte, error)
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func (c Config) Valid() error {
	if !identifier.MatchString(c.ID) || c.Name == "" || c.Version == "" || c.MaxFileBytes < 1 || c.MaxFileBytes > MaxFileSize || len(c.Categories) < 1 || len(c.Categories) > 32 || c.TitleMaxLength < 1 || c.TitleMaxLength > 1024 || c.DescriptionMaxLength < 1 || c.DescriptionMaxLength > 16000 || c.MaxTags < 0 || c.MaxTags > 64 || c.TagMaxLength < 1 || c.TagMaxLength > 256 || c.Validate == nil || c.Title == "" || len(c.Title) > 120 || len(c.Description) > 1000 || len(c.MediaTypes) < 1 || len(c.MediaTypes) > 4 {
		return fmt.Errorf("invalid collection configuration")
	}
	types := map[string]bool{}
	for _, typ := range c.MediaTypes {
		if types[typ] || (typ != "application/pdf" && typ != "image/png" && typ != "image/jpeg" && typ != "text/plain") {
			return fmt.Errorf("invalid collection media type")
		}
		types[typ] = true
	}
	seen := map[string]bool{}
	for _, option := range c.Categories {
		if !storageKey.MatchString(option.Value) || option.Label == "" || len(option.Label) > 120 || seen[option.Value] {
			return fmt.Errorf("invalid collection category")
		}
		seen[option.Value] = true
	}
	return nil
}
func (c Config) category(value string) bool {
	for _, option := range c.Categories {
		if option.Value == value {
			return true
		}
	}
	return false
}
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
