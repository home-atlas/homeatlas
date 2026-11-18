package application

import (
	"context"
	"os"

	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type PendingOriginal interface {
	Publish(context.Context, unitofwork.Transaction) error
	Abort()
}
type RemoteOriginals interface {
	Upload(context.Context, string, string, string, *os.File, int64, string) (PendingOriginal, error)
	Open(context.Context, string, string) (*os.File, func(), error)
}
type Originals interface {
	Create(string) (*os.File, error)
	Open(string) (*os.File, error)
	Remove(string) error
	SyncDirectory() error
	Inspect(*os.File, string, int64) (string, error)
	Close() error
}
