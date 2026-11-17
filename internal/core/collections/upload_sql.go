package collections

import (
	"context"
	"encoding/json"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

func (m *Module) persistUpload(ctx context.Context, item Item, remote *objectstore.Pending) error {
	p, _ := access.Current(ctx)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return uploadFailure("items_unavailable", err)
	}
	defer tx.Rollback()
	if err := m.authorizeMutation(ctx, tx.Tx, "write"); err != nil {
		return uploadFailure("permission_denied", err)
	}
	// Non-HTTP callers need the same activation check as the module HTTP registry.
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM module_states m WHERE m.id=? AND m.enabled=1
 AND NOT EXISTS(SELECT 1 FROM household_module_states h WHERE h.module_id=m.id AND h.household_id=? AND h.enabled=0))`, m.config.ID, p.HouseholdID).Scan(&enabled); err != nil {
		return uploadFailure("items_unavailable", err)
	}
	if !enabled {
		return uploadFailure("permission_denied", nil)
	}
	tags, _ := json.Marshal(item.Tags)
	_, err = tx.ExecContext(ctx, `INSERT INTO collection_items
  (id,household_id,owner_id,title,category,created_at,description,tags,original_filename,media_type,size_bytes,sha256,file_key)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, p.HouseholdID, p.UserID, item.Title, item.Category, item.CreatedAt, item.Description, string(tags), item.Filename, item.MediaType, item.Size, item.SHA256, item.ID)
	if err != nil {
		return uploadFailure("items_unavailable", err)
	}
	if remote != nil {
		if err := remote.Publish(ctx, tx.Tx); err != nil {
			return uploadFailure("storage_unavailable", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return uploadFailure("items_unavailable", err)
	}
	return nil
}
