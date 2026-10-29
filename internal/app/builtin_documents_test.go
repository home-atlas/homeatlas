package app

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"

	"github.com/home-atlas/homeatlas/internal/app/testdata/legacydocuments"
	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

func init() {
	defaultModules = []string{"documents"}
	builtInModules = map[string]moduleDefinition{
		"documents": {
			backup: func(dir string, directory access.Directory) backup.Participant {
				return backup.Participant{Provider: documents.NewBackup(dir, directory), LegacyPrefix: "documents", LegacyCountField: "documents"}
			},
			ownerCapabilities:     []string{documents.Read, documents.Write, documents.Share},
			delegableCapabilities: []string{documents.Read, documents.Write, documents.Share},
			register:              registerDocuments,
		},
	}

}

func registerDocuments(ctx context.Context, db *sql.DB, dataDir string, accounts *identity.Service, registry *modules.Registry) (io.Closer, error) {
	docs, err := documents.New(db, filepath.Join(dataDir, "documents"), accounts)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			docs.Close()
		}
	}()
	// Grant defaults only at first installation, including owners created with Core alone.
	// Keeping the condition inside a recorded migration preserves later revocations.
	if err := database.Apply(ctx, db, "app.documents-installation-permissions", []database.Migration{{Version: 1, SQL: `
 INSERT OR IGNORE INTO identity_permissions(user_id,household_id,capability)
 SELECT m.user_id,m.household_id,c.capability FROM identity_memberships m
 CROSS JOIN (SELECT 'documents.read' capability UNION ALL SELECT 'documents.write' UNION ALL SELECT 'documents.share') c
 WHERE m.role='owner' AND NOT EXISTS(SELECT 1 FROM module_states WHERE id='documents');`}}); err != nil {
		return nil, err
	}
	if err := registry.Register(ctx, docs); err != nil {
		return nil, err
	}
	// Upgrade existing owners once; restarting must not restore a revoked permission.
	if err := database.Apply(ctx, db, "app.document-owner-permissions", []database.Migration{{Version: 1, SQL: `
 INSERT OR IGNORE INTO identity_permissions(user_id,household_id,capability)
 SELECT m.user_id,m.household_id,'documents.write' FROM identity_memberships m
 JOIN identity_permissions p ON p.user_id=m.user_id AND p.household_id=m.household_id AND p.capability='documents.read'
 WHERE m.role='owner';`}}); err != nil {
		return nil, err
	}
	if err := database.Apply(ctx, db, "app.household-sharing-permissions", []database.Migration{{Version: 1, SQL: `
 INSERT OR IGNORE INTO identity_permissions(user_id,household_id,capability)
 SELECT user_id,household_id,'household.manage' FROM identity_memberships WHERE role='owner';
 INSERT OR IGNORE INTO identity_permissions(user_id,household_id,capability)
 SELECT m.user_id,m.household_id,'documents.share' FROM identity_memberships m
 JOIN identity_permissions p ON p.user_id=m.user_id AND p.household_id=m.household_id AND p.capability='documents.read'
 WHERE m.role='owner';`}}); err != nil {
		return nil, err
	}

	ready = true
	return docs, nil
}
