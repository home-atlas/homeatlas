package app

import (
	"context"
	"database/sql"
	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"io"
)

// Built-ins are trusted application policy; manifest declarations never grant permissions.
type moduleDefinition struct {
	backup                                   func(string, access.Directory) backup.Participant
	ownerCapabilities, delegableCapabilities []string
	register                                 func(context.Context, *sql.DB, string, *identity.Service, *modules.Registry) (io.Closer, error)
}

var builtInModules = map[string]moduleDefinition{}
var defaultModules []string
