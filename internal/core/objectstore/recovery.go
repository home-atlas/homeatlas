package objectstore

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/objectstore/adapters/sqlite"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
	"github.com/home-atlas/homeatlas/internal/core/recovery"
)

// Reconcile is startup infrastructure, called under the exclusive data lock.
// Unknown/removed packages and absent schemas are preserved rather than guessed.
func (s *Store) Reconcile(ctx context.Context, collections []Collection) error {
	repository := sqlite.New(s.db)
	for _, collection := range collections {
		if !domain.SafeID.MatchString(collection.ID) || !domain.TableName.MatchString(collection.Table) {
			return Invalid
		}
		present, err := repository.Present(ctx, collection.Table)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if err := s.privateCollectionDirectory(collection.Directory); err != nil {
			return err
		}
		keys, err := repository.LocalKeys(ctx, collection)
		if err != nil {
			return err
		}
		count, err := recovery.Originals(ctx, collection.Directory, keys)
		if err != nil {
			return fmt.Errorf("reconcile %s originals: %w", collection.ID, err)
		}
		if count > 0 {
			slog.Warn("unreferenced originals preserved in private .recovered directory", "module", collection.ID, "files", count)
		}
	}
	cleanup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.RecoverPending(cleanup, collections); err != nil {
		slog.Warn("original cleanup remains pending; retry on restart or storage move", "error", err)
	}
	return ctx.Err()
}

func (s *Store) privateCollectionDirectory(directory string) error {
	rel, err := filepath.Rel(s.dir, directory)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Invalid
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	path := ""
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := root.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("collection originals must use private directories, without symlinks")
		}
	}
	return nil
}
