package application

import (
	"errors"

	"github.com/home-atlas/homeatlas/internal/core/collections/domain"
)

type Item = domain.Item
type Metadata = domain.Metadata
type Recipient = domain.Recipient
type Option = domain.Option
type Definition = domain.Definition

const metadataLimit = domain.MetadataLimit

var ErrValidatorUnavailable = errors.New("collection validator unavailable")
