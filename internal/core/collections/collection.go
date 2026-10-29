package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

type Module struct {
	db        *scopedDB
	config    Config
	files     *os.Root
	uploads   chan struct{}
	directory access.Directory
}

func New(db *sql.DB, directory string, members access.Directory, config Config) (*Module, error) {
	if err := config.Valid(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	files, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Module{db: &scopedDB{db, config}, config: config, files: files, uploads: make(chan struct{}, 2), directory: members}, nil
}

func (m *Module) Close() error { return m.files.Close() }

func (m *Module) permission(action string) string { return m.config.ID + "." + action }
func (m *Module) Manifest() modules.Manifest {
	presentation, _ := json.Marshal(m.config.Definition)
	return modules.Manifest{Collection: presentation, ID: m.config.ID, Name: m.config.Name, Version: m.config.Version, ContractVersion: modules.ContractVersion, Runtime: "wasi-preview1", Capabilities: []string{m.permission("read"), m.permission("write"), m.permission("share")}, Dependencies: []string{}, Navigation: modules.Navigation{Label: m.config.Name, Path: "/" + m.config.ID}}
}

func (m *Module) Migrations() []database.Migration {
	migrations := []database.Migration{{Version: 1, SQL: `
		CREATE TABLE collection_items (
			id TEXT PRIMARY KEY, household_id TEXT NOT NULL, owner_id TEXT NOT NULL,
			title TEXT NOT NULL, category TEXT NOT NULL DEFAULT '__collection_default__',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')));
		CREATE INDEX collection_access ON collection_items (household_id, owner_id);
		CREATE VIRTUAL TABLE collection_fts USING fts5(title, content='collection_items', content_rowid='rowid');
		CREATE TRIGGER collection_ai AFTER INSERT ON collection_items BEGIN
			INSERT INTO collection_fts(rowid, title) VALUES (new.rowid, new.title); END;
		CREATE TRIGGER collection_ad AFTER DELETE ON collection_items BEGIN
			INSERT INTO collection_fts(collection_fts, rowid, title) VALUES ('delete', old.rowid, old.title); END;
		CREATE TRIGGER collection_au AFTER UPDATE ON collection_items BEGIN
			INSERT INTO collection_fts(collection_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
			INSERT INTO collection_fts(rowid, title) VALUES (new.rowid, new.title); END;`},
		{Version: 2, SQL: `
		ALTER TABLE collection_items ADD COLUMN description TEXT NOT NULL DEFAULT '';
		ALTER TABLE collection_items ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
		ALTER TABLE collection_items ADD COLUMN original_filename TEXT NOT NULL DEFAULT '';
		ALTER TABLE collection_items ADD COLUMN media_type TEXT NOT NULL DEFAULT '';
		ALTER TABLE collection_items ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0);
		ALTER TABLE collection_items ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';
		ALTER TABLE collection_items ADD COLUMN file_key TEXT NOT NULL DEFAULT '';
		CREATE UNIQUE INDEX collection_file_key ON collection_items(file_key) WHERE file_key != '';
		DROP TRIGGER collection_ai;
		DROP TRIGGER collection_ad;
		DROP TRIGGER collection_au;
		DROP TABLE collection_fts;
		CREATE VIRTUAL TABLE collection_fts USING fts5(title, description, tags, content='collection_items', content_rowid='rowid');
		CREATE TRIGGER collection_ai AFTER INSERT ON collection_items BEGIN
			INSERT INTO collection_fts(rowid, title, description, tags) VALUES(new.rowid, new.title, new.description, new.tags); END;
		CREATE TRIGGER collection_ad AFTER DELETE ON collection_items BEGIN
			INSERT INTO collection_fts(collection_fts, rowid, title, description, tags) VALUES('delete', old.rowid, old.title, old.description, old.tags); END;
		CREATE TRIGGER collection_au AFTER UPDATE ON collection_items BEGIN
			INSERT INTO collection_fts(collection_fts, rowid, title, description, tags) VALUES('delete', old.rowid, old.title, old.description, old.tags);
			INSERT INTO collection_fts(rowid, title, description, tags) VALUES(new.rowid, new.title, new.description, new.tags); END;
		INSERT INTO collection_fts(collection_fts) VALUES('rebuild');`},
		{Version: 3, SQL: `
 CREATE TABLE collection_grants (
   item_id TEXT NOT NULL REFERENCES collection_items(id) ON DELETE CASCADE,
   household_id TEXT NOT NULL, user_id TEXT NOT NULL, member_id TEXT NOT NULL,
   created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
   PRIMARY KEY(item_id,user_id));
 CREATE INDEX collection_grants_recipient ON collection_grants(household_id,member_id,item_id);`}}
	for i := range migrations {
		migrations[i].SQL = m.config.sql(migrations[i].SQL)
	}
	return migrations
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

const columns = `d.id, d.title, d.category, d.created_at, d.description, d.tags, d.original_filename, d.media_type, d.size_bytes, d.sha256, (d.file_key != ''), d.owner_id, d.file_key`

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var d Item
	var tags string
	err := row.Scan(&d.ID, &d.Title, &d.Category, &d.CreatedAt, &d.Description, &tags, &d.Filename, &d.MediaType, &d.Size, &d.SHA256, &d.HasFile, &d.OwnerID, &d.fileKey)
	if err == nil {
		err = json.Unmarshal([]byte(tags), &d.Tags)
	}
	return d, err
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, ok := access.Current(req.Context())
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
	statement := "SELECT " + columns + " FROM collection_items d "
	if query != "" {
		statement += "JOIN collection_fts ON collection_fts.rowid = d.rowid "
	}
	statement += "WHERE " + readable + " "
	args := readArgs(p)
	if query != "" {
		statement += "AND collection_fts MATCH ? "
		args = append(args, literalQuery(query))
	}
	if category != "" {
		statement += "AND d.category = ? "
		args = append(args, category)
	}
	statement += "ORDER BY d.created_at DESC, d.id LIMIT 50 OFFSET ?"
	args = append(args, offset)
	rows, err := m.db.QueryContext(req.Context(), statement, args...)
	if err != nil {
		modules.JSON(w, 500, map[string]string{"error": "items_unavailable"})
		return
	}
	defer rows.Close()
	result := []Item{}
	for rows.Next() {
		d, err := scanItem(rows)
		if err != nil {
			modules.JSON(w, 500, map[string]string{"error": "items_unavailable"})
			return
		}
		result = append(result, d)
	}
	if rows.Err() != nil {
		modules.JSON(w, 500, map[string]string{"error": "items_unavailable"})
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
	// Quoting makes input literal FTS text rather than exposing the FTS query language.
	query = literalQuery(query)
	rows, err := m.db.QueryContext(ctx, `SELECT d.id, d.title FROM collection_fts
		JOIN collection_items d ON d.rowid = collection_fts.rowid
		WHERE collection_fts MATCH ? AND `+readable+`
		ORDER BY rank LIMIT 50`, append([]any{query}, readArgs(p)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var match modules.SearchResult
		if err := rows.Scan(&match.ID, &match.Title); err != nil {
			return nil, err
		}
		match.Module = m.config.ID
		match.Path = "/" + m.config.ID + "/" + match.ID
		result = append(result, match)
	}
	return result, rows.Err()
}

func literalQuery(query string) string { return `"` + strings.ReplaceAll(query, `"`, `""`) + `"` }

// Ownership and explicit recipient grants always stay within the session household.
const readable = `d.household_id=? AND (d.owner_id=? OR EXISTS(
 SELECT 1 FROM collection_grants s WHERE s.item_id=d.id AND s.household_id=d.household_id AND s.member_id=? AND s.user_id=?))`

func readArgs(p access.Principal) []any {
	return []any{p.HouseholdID, p.UserID, p.MembershipID, p.UserID}
}
