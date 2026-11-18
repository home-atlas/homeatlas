package application

import "github.com/home-atlas/homeatlas/internal/core/objectstore/domain"

type S3Config = domain.S3Config
type Settings = domain.Settings
type Collection = domain.Collection
type MoveStats = domain.MoveStats
type MoveResult = domain.MoveResult
type MoveItem = domain.MoveItem
type Obsolete = domain.Obsolete
type Location = domain.Location

var (
	Invalid     = domain.Invalid
	Conflict    = domain.Conflict
	Unavailable = domain.Unavailable
)
