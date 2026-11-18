package application

import (
	"context"
	"io"
	"os"

	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository interface {
	Begin(ctx context.Context) (unitofwork.Transaction, error)
	Profile(ctx context.Context, ID string) (string, []byte, error)
	Settings(ctx context.Context, tx unitofwork.Transaction, home string) (string, string, error)
	Profiles(ctx context.Context, home string) (map[string][]byte, error)
	SaveSettings(ctx context.Context, tx unitofwork.Transaction, house, ID string, sealed []byte, refresh map[string][]byte, revision string) error
	Publish(ctx context.Context, tx unitofwork.Transaction, p domain.Location) error
	Location(ctx context.Context, Module, Key string) (Location, error)
	Present(ctx context.Context, table string) (bool, error)
	MoveStats(ctx context.Context, house string, c Collection) (MoveStats, error)
	PendingCleanup(ctx context.Context, house string) (int, error)
	NextCleanup(ctx context.Context, house string) (Obsolete, error)
	NextMove(ctx context.Context, house string, c Collection, toRemote bool) (MoveItem, error)
	PublishMove(ctx context.Context, tx unitofwork.Transaction, house string, c Collection, item MoveItem, next string, old Obsolete, pending *domain.Location) (bool, error)
	FinishCleanup(ctx context.Context, house, ID string) error
}
type Remote interface {
	Put(context.Context, domain.S3Config, string, io.ReadSeeker, int64) error
	Delete(context.Context, domain.S3Config, string) error
	Probe(context.Context, string, domain.S3Config) error
	Fetch(context.Context, domain.S3Config, string, int64, string) (*os.File, func(), error)
}
type Cipher interface {
	Crypt(string, string, []byte, bool) ([]byte, error)
}
