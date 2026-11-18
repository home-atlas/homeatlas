package application

import (
	"context"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	modules "github.com/home-atlas/homeatlas/internal/core/modules/domain"
)

type Module struct {
	repository Repository
	config     Config
	files      Originals
	uploads    chan struct{}
	directory  access.Directory
}

func (m *Module) Close() error { return m.files.Close() }

func (m *Module) Permission(action string) string { return m.config.ID + "." + action }

func New(repository Repository, files Originals, directory access.Directory, config Config) (*Module, error) {
	if err := config.Valid(); err != nil {
		return nil, err
	}
	return &Module{repository: repository, files: files, directory: directory, config: config, uploads: make(chan struct{}, 2)}, nil
}
func (m *Module) Search(ctx context.Context, query string) ([]modules.SearchResult, error) {
	result := []modules.SearchResult{}
	p, ok := access.Current(ctx)
	if !ok || !access.Allows(ctx, m.Permission("read")) || strings.TrimSpace(query) == "" {
		return result, nil
	}
	return m.repository.Search(ctx, p, query)
}
