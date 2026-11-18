package objectstore

import (
	"context"
	"database/sql"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/adapters/localcrypto"
	s3adapter "github.com/home-atlas/homeatlas/internal/core/objectstore/adapters/s3"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/adapters/sqlite"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/application"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
)

type Store struct {
	*application.Store
	db     *sql.DB
	dir    string
	remote *s3adapter.Client
}

func New(ctx context.Context, db *sql.DB, dir string) (*Store, error) {
	if err := sqlite.Migrate(ctx, db); err != nil {
		return nil, err
	}
	remote := s3adapter.New(dir)
	return &Store{Store: application.New(sqlite.New(db), remote, localcrypto.New(dir)), db: db, dir: dir, remote: remote}, nil
}
func (s *Store) SetHTTPClient(client aws.HTTPClient) { s.remote.HTTPClient = client }
func (s *Store) OpenFrom(ctx context.Context, q backup.Queryer, module, key string) (*os.File, func(), error) {
	location, home, sealed, err := sqlite.SnapshotLocation(ctx, q, module, key)
	if err != nil {
		return nil, func() {}, err
	}
	return s.Store.OpenSnapshot(ctx, location, home, location.Profile, sealed)
}

type S3Config = domain.S3Config
type Settings = domain.Settings
type Collection = domain.Collection
type MoveStats = domain.MoveStats
type MoveResult = domain.MoveResult
type Pending = application.Pending

var (
	Invalid     = domain.Invalid
	Conflict    = domain.Conflict
	Unavailable = domain.Unavailable
)
