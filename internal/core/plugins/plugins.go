package plugins

import (
	"context"
	"database/sql"
	"sync"

	httpadapter "github.com/home-atlas/homeatlas/internal/core/plugins/adapters/http"
	"github.com/home-atlas/homeatlas/internal/core/plugins/adapters/packages"
	"github.com/home-atlas/homeatlas/internal/core/plugins/adapters/sqlite"
	"github.com/home-atlas/homeatlas/internal/core/plugins/adapters/wasi"
	"github.com/home-atlas/homeatlas/internal/core/plugins/application"
	"github.com/home-atlas/homeatlas/internal/core/plugins/domain"
)

type Store struct {
	*application.Store
	db *sql.DB
}

func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	if err := sqlite.Migrate(ctx, db); err != nil {
		return nil, err
	}
	path, err := sqlite.DatabasePath(ctx, db)
	if err != nil {
		return nil, err
	}
	return &Store{application.NewStore(sqlite.New(db), path), db}, nil
}

type Engine struct {
	Store  *Store
	once   sync.Once
	runner *application.Runner
}

func (e *Engine) initialize() {
	e.once.Do(func() {
		e.runner = application.NewRunner(&wasi.Engine{Discard: func() {
			if e.Store != nil {
				e.Store.DiscardCache()
			}
		}})
	})
}
func (e *Engine) Run(ctx context.Context, p *Package, view string, input map[string]string) (Result, error) {
	e.initialize()
	var store *application.Store
	if e.Store != nil {
		store = e.Store.Store
	}
	return e.runner.Run(ctx, store, p, view, input)
}
func (e *Engine) Close() error           { e.initialize(); return e.runner.Close() }
func (e *Engine) Pause() func()          { e.initialize(); return e.runner.Pause() }
func Read(data []byte) (*Package, error) { return packages.Read(data) }
func Install(ctx context.Context, dir, archive, digest string, approved []string) (*Package, error) {
	return packages.Install(ctx, dir, archive, digest, approved)
}
func Load(dir string) ([]*Package, error) { return packages.Load(dir) }

type Package = domain.Package
type Manifest = domain.Manifest
type View = domain.View
type Field = domain.Field
type StorageRequest = domain.StorageRequest
type StorageResponse = domain.StorageResponse
type SharedRecord = domain.SharedRecord
type SharedChange = domain.SharedChange
type GrantKey = domain.GrantKey
type SharedGrant = domain.SharedGrant
type Result = domain.Result
type Module = httpadapter.Module
type Backup = sqlite.Backup
type CollectionBackup = sqlite.CollectionBackup

const (
	MaxPackageBytes   = domain.MaxPackageBytes
	MemoryPages       = domain.MemoryPages
	ExecutionTimeout  = domain.ExecutionTimeout
	StorageValueBytes = domain.StorageValueBytes
	StorageRPCBytes   = domain.StorageRPCBytes
	StorageCalls      = domain.StorageCalls
	StoragePageKeys   = domain.StoragePageKeys
)

var (
	ErrStorageDenied   = domain.ErrStorageDenied
	ErrStorageConflict = domain.ErrStorageConflict
	ErrBusy            = domain.ErrBusy
)
