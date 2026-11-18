package remotestorage

import (
	"context"
	"os"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/collections/application"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

type Store struct{ store *objectstore.Store }

func New(store *objectstore.Store) *Store { return &Store{store} }
func (s *Store) Upload(ctx context.Context, home, module, key string, file *os.File, size int64, digest string) (application.PendingOriginal, error) {
	pending, err := s.store.Upload(ctx, home, module, key, file, size, digest)
	if pending == nil {
		return nil, err
	}
	return pending, err
}
func (s *Store) Open(ctx context.Context, module, key string) (*os.File, func(), error) {
	return s.store.Open(ctx, module, key)
}
func (s *Store) OpenFrom(ctx context.Context, q backup.Queryer, module, key string) (*os.File, func(), error) {
	return s.store.OpenFrom(ctx, q, module, key)
}
