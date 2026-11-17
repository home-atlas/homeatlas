package collections

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

type Module struct {
	repository Repository
	config     Config
	files      *os.Root
	uploads    chan struct{}
	directory  access.Directory
}

func (m *Module) Close() error { return m.files.Close() }

func (m *Module) permission(action string) string { return m.config.ID + "." + action }
func (m *Module) Manifest() modules.Manifest {
	presentation, _ := json.Marshal(m.config.Definition)
	return modules.Manifest{Collection: presentation, ID: m.config.ID, Name: m.config.Name, Version: m.config.Version, ContractVersion: modules.ContractVersion, Runtime: "wasi-preview1", Capabilities: []string{m.permission("read"), m.permission("write"), m.permission("share")}, Dependencies: []string{}, Navigation: modules.Navigation{Label: m.config.Name, Path: "/" + m.config.ID}}
}

type Item struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	CreatedAt   string   `json:"createdAt"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Filename    string   `json:"filename"`
	MediaType   string   `json:"mediaType"`
	Size        int64    `json:"size"`
	SHA256      string   `json:"sha256"`
	OwnerID     string   `json:"ownerId"`
	OwnerName   string   `json:"ownerName"`
	fileKey     string
	HasFile     bool `json:"hasFile"`
}

func (m *Module) Routes() []modules.Route {
	return []modules.Route{
		{Method: http.MethodGet, Path: "/", Permission: m.permission("read"), Handler: m.list},
		{Method: http.MethodPost, Path: "/", Permission: m.permission("write"), Handler: m.upload},
		{Method: http.MethodGet, Path: "/{id}", Permission: m.permission("read"), Handler: m.detail},
		{Method: http.MethodPatch, Path: "/{id}", Permission: m.permission("write"), Handler: m.update},
		{Method: http.MethodGet, Path: "/{id}/download", Permission: m.permission("read"), Handler: m.download},
		{Method: http.MethodGet, Path: "/{id}/shares", Permission: m.permission("read"), Handler: m.shares},
		{Method: http.MethodPut, Path: "/{id}/shares/{user}", Permission: m.permission("share"), Handler: m.share},
		{Method: http.MethodDelete, Path: "/{id}/shares/{user}", Permission: m.permission("share"), Handler: m.unshare},
	}
}

func (m *Module) SearchPermission() string { return m.permission("read") }

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	_, ok := access.Current(req.Context())
	if !ok || !access.Allows(req.Context(), m.permission("read")) {
		modules.Authorize(w, req, m.permission("read"))
		return
	}
	offset := 0
	if value := req.URL.Query().Get("offset"); value != "" {
		var err error
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 || offset > 1_000_000 {
			fail(w, 400, "invalid_query")
			return
		}
	}
	query := strings.TrimSpace(req.URL.Query().Get("q"))
	category := req.URL.Query().Get("category")
	if len(query) > 200 || (category != "" && !m.config.category(category)) {
		fail(w, 400, "invalid_query")
		return
	}
	result, err := m.List(req.Context(), ListInput{Query: query, Category: category, Offset: offset})
	if err != nil {
		itemError(w, err)
		return
	}
	modules.JSON(w, 200, result)
}

func (m *Module) Search(ctx context.Context, query string) ([]modules.SearchResult, error) {
	result := []modules.SearchResult{}
	p, ok := access.Current(ctx)
	if !ok || !access.Allows(ctx, m.permission("read")) || strings.TrimSpace(query) == "" {
		return result, nil
	}
	return m.repository.search(ctx, p, literalQuery(query))
}

func literalQuery(query string) string { return `"` + strings.ReplaceAll(query, `"`, `""`) + `"` }
