package sqlite

import (
	"database/sql"

	"github.com/home-atlas/homeatlas/internal/core/collections/application"
	"github.com/home-atlas/homeatlas/internal/core/collections/domain"
)

func New(db *sql.DB, config application.Config) *Repository {
	return &Repository{db: &scopedDB{db, Config{config}}}
}

var _ application.Repository = (*Repository)(nil)

type Item = domain.Item
type Metadata = domain.Metadata
type Recipient = domain.Recipient
type ListInput = application.ListInput

const MaxFileSize = domain.MaxFileSize
