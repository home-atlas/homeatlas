package sqlite

import (
	"github.com/home-atlas/homeatlas/internal/core/plugins/application"
	"github.com/home-atlas/homeatlas/internal/core/plugins/domain"
)

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

const (
	StorageValueBytes = domain.StorageValueBytes
	StorageRPCBytes   = domain.StorageRPCBytes
	StorageCalls      = domain.StorageCalls
	StoragePageKeys   = domain.StoragePageKeys
)

var (
	ErrStorageDenied   = domain.ErrStorageDenied
	ErrStorageConflict = domain.ErrStorageConflict
	errStorageInvalid  = domain.ErrStorageInvalid
	storageKey         = domain.StorageKey
	storageIdentity    = domain.StorageIdentity
)

var _ application.Repository = (*Repository)(nil)
