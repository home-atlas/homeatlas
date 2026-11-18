package sqlite

import (
	"github.com/home-atlas/homeatlas/internal/core/objectstore/application"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
)

type S3Config = domain.S3Config
type Settings = domain.Settings
type Collection = domain.Collection
type MoveStats = domain.MoveStats
type MoveResult = domain.MoveResult
type MoveItem = domain.MoveItem
type Obsolete = domain.Obsolete
type Location = domain.Location

var _ application.Repository = (*Repository)(nil)
