// Package collections composes domain/application with trusted host adapters.
package collections

import (
	"database/sql"
	"encoding/json"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/collections/adapters/filesystem"
	httpadapter "github.com/home-atlas/homeatlas/internal/core/collections/adapters/http"
	remotestorage "github.com/home-atlas/homeatlas/internal/core/collections/adapters/objectstore"
	"github.com/home-atlas/homeatlas/internal/core/collections/adapters/sqlite"
	"github.com/home-atlas/homeatlas/internal/core/collections/application"
	"github.com/home-atlas/homeatlas/internal/core/collections/domain"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

type Module struct {
	*application.Module
	*httpadapter.Transport
	config Config
}

func New(db *sql.DB, directory string, members access.Directory, config Config) (*Module, error) {
	if err := config.Valid(); err != nil {
		return nil, err
	}
	files, err := filesystem.New(directory)
	if err != nil {
		return nil, err
	}
	app, err := application.New(sqlite.New(db, config), files, members, config)
	if err != nil {
		files.Close()
		return nil, err
	}
	return &Module{app, httpadapter.New(app, config), config}, nil
}
func (m *Module) Manifest() modules.Manifest {
	presentation, _ := json.Marshal(m.config.Definition)
	return modules.Manifest{Collection: presentation, ID: m.config.ID, Name: m.config.Name, Version: m.config.Version, ContractVersion: modules.ContractVersion, Runtime: "wasi-preview1", Capabilities: []string{m.Permission("read"), m.Permission("write"), m.Permission("share")}, Dependencies: []string{}, Navigation: modules.Navigation{Label: m.config.Name, Path: "/" + m.config.ID}}
}
func (m *Module) Migrations() []database.Migration { return sqlite.Migrations(m.config) }
func (m *Module) SearchPermission() string         { return m.Permission("read") }
func NewBackup(dir string, members access.Directory, config Config) backup.Provider {
	return sqlite.NewBackup(dir, members, config)
}
func StorageCollection(config Config, directory string) objectstore.Collection {
	return sqlite.StorageCollection(config, directory)
}
func RemoteStorage(store *objectstore.Store) application.RemoteOriginals {
	if store == nil {
		return nil
	}
	return remotestorage.New(store)
}

type Item = domain.Item
type Option = domain.Option
type Definition = domain.Definition
type Config = application.Config
type UploadInput = application.UploadInput
type UploadError = application.UploadError
type ListInput = application.ListInput

const MaxFileSize = domain.MaxFileSize

var ErrValidatorUnavailable = application.ErrValidatorUnavailable
