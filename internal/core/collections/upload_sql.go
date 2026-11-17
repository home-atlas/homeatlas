package collections

import (
	"context"
	"encoding/json"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

func (s *sqliteRepository) publish(ctx context.Context, p access.Principal, item Item, remote *objectstore.Pending, authorize func(unitofwork.Transaction) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return uploadFailure("items_unavailable", err)
	}
	defer tx.Rollback()
	if err := authorize(tx); err != nil {
		return uploadFailure("permission_denied", err)
	}
	tags, _ := json.Marshal(item.Tags)
	_, err = tx.ExecContext(ctx, `INSERT INTO collection_items
  (id,household_id,owner_id,title,category,created_at,description,tags,original_filename,media_type,size_bytes,sha256,file_key)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, p.HouseholdID, p.UserID, item.Title, item.Category, item.CreatedAt, item.Description, string(tags), item.Filename, item.MediaType, item.Size, item.SHA256, item.ID)
	if err != nil {
		return uploadFailure("items_unavailable", err)
	}
	if remote != nil {
		if err := remote.Publish(ctx, tx); err != nil {
			return uploadFailure("storage_unavailable", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return uploadFailure("items_unavailable", err)
	}
	return nil
}
