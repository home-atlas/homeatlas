package application

import "github.com/home-atlas/homeatlas/internal/core/modules/domain"

type Manifest = domain.Manifest
type Status = domain.Status
type SearchResult = domain.SearchResult

const ContractVersion = domain.ContractVersion

var (
	ValidID              = domain.ValidID
	ErrUnknownModule     = domain.ErrUnknownModule
	ErrUnavailable       = domain.ErrUnavailable
	ErrDependency        = domain.ErrDependency
	ErrHouseholdRequired = domain.ErrHouseholdRequired
)
