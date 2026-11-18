package application

import (
	"context"
	"errors"
	"os"

	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

func (m *Module) OpenFile(ctx context.Context, key string) (*os.File, func(), error) {
	if m.config.Storage != nil {
		f, cleanup, err := m.config.Storage.Open(ctx, m.config.ID, key)
		if !errors.Is(err, unitofwork.ErrNotFound) {
			return f, cleanup, err
		}
	}
	f, err := m.files.Open(key)
	return f, func() {
		if f != nil {
			f.Close()
		}
	}, err
}
