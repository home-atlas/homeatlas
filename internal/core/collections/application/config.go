// Package collections implements the host-owned file/metadata/ACL primitives
// available to isolated plugins. Package rules run through Validate, never SQL.
package application

import (
	"context"
	"fmt"

	"github.com/home-atlas/homeatlas/internal/core/collections/domain"
)

type Config struct {
	Storage           RemoteOriginals
	ID, Name, Version string
	Definition
	Validate func(context.Context, []byte) ([]byte, error)
}

func (c Config) Valid() error {
	if c.Validate == nil {
		return fmt.Errorf("invalid collection configuration")
	}
	return domain.ValidateDefinition(c.ID, c.Name, c.Version, c.Definition)
}

func (c Config) Category(value string) bool {
	for _, option := range c.Categories {
		if option.Value == value {
			return true
		}
	}
	return false
}
