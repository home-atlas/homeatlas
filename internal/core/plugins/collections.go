package plugins

import (
	"context"
	"github.com/home-atlas/homeatlas/internal/core/collections"
	"path/filepath"
)

// Collection validators remain isolated commands: no SQL, files, host handles
// or credentials enter WASI. The host applies the file/ACL protocol itself.
func CollectionConfig(p *Package, engine *Engine) collections.Config {
	return collections.Config{ID: p.Manifest.ID, Name: p.Manifest.Name, Version: p.Manifest.Version, Definition: *p.Manifest.Collection, Validate: func(ctx context.Context, raw []byte) ([]byte, error) {
		result, err := engine.Run(ctx, p, "validate", map[string]string{"metadata": string(raw)})
		if err != nil {
			return nil, err
		}
		return []byte(result.Text), nil
	}}
}
func CollectionDirectory(dataDir string, p *Package) string {
	if p.Manifest.Collection.LegacySnapshot {
		return filepath.Join(dataDir, p.Manifest.ID)
	}
	return filepath.Join(dataDir, "plugin-files", p.Manifest.ID)
}
