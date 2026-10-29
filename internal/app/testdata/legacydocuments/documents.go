package documents

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

const Read = "documents.read"
const Write = "documents.write"
const Share = "documents.share"

type Module struct {
	db        *sql.DB
	files     *os.Root
	uploads   chan struct{}
	directory access.Directory
}

func New(db *sql.DB, directory string, members access.Directory) (*Module, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	files, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Module{db: db, files: files, uploads: make(chan struct{}, 2), directory: members}, nil
}

func (m *Module) Close() error { return m.files.Close() }

func (m *Module) Manifest() modules.Manifest {
	return modules.Manifest{ID: "documents", Name: "Documents", Version: "0.3.0", ContractVersion: modules.ContractVersion,
		Capabilities: []string{Read, Write, Share}, Dependencies: []string{}, Navigation: modules.Navigation{Label: "Documentos", Path: "/documents"}}
}

func (m *Module) Migrations() []database.Migration {
	return []database.Migration{{Version: 1, SQL: `
		CREATE TABLE documents (
			id TEXT PRIMARY KEY, household_id TEXT NOT NULL, owner_id TEXT NOT NULL,
			title TEXT NOT NULL, category TEXT NOT NULL DEFAULT 'otros',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')));
		CREATE INDEX documents_access ON documents (household_id, owner_id);
		CREATE VIRTUAL TABLE documents_fts USING fts5(title, content='documents', content_rowid='rowid');
		CREATE TRIGGER documents_ai AFTER INSERT ON documents BEGIN
			INSERT INTO documents_fts(rowid, title) VALUES (new.rowid, new.title); END;
		CREATE TRIGGER documents_ad AFTER DELETE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title) VALUES ('delete', old.rowid, old.title); END;
		CREATE TRIGGER documents_au AFTER UPDATE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title) VALUES ('delete', old.rowid, old.title);
			INSERT INTO documents_fts(rowid, title) VALUES (new.rowid, new.title); END;`},
		{Version: 2, SQL: `
		ALTER TABLE documents ADD COLUMN description TEXT NOT NULL DEFAULT '';
		ALTER TABLE documents ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
		ALTER TABLE documents ADD COLUMN original_filename TEXT NOT NULL DEFAULT '';
		ALTER TABLE documents ADD COLUMN media_type TEXT NOT NULL DEFAULT '';
		ALTER TABLE documents ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0);
		ALTER TABLE documents ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';
		ALTER TABLE documents ADD COLUMN file_key TEXT NOT NULL DEFAULT '';
		CREATE UNIQUE INDEX documents_file_key ON documents(file_key) WHERE file_key != '';
		DROP TRIGGER documents_ai;
		DROP TRIGGER documents_ad;
		DROP TRIGGER documents_au;
		DROP TABLE documents_fts;
		CREATE VIRTUAL TABLE documents_fts USING fts5(title, description, tags, content='documents', content_rowid='rowid');
		CREATE TRIGGER documents_ai AFTER INSERT ON documents BEGIN
			INSERT INTO documents_fts(rowid, title, description, tags) VALUES(new.rowid, new.title, new.description, new.tags); END;
		CREATE TRIGGER documents_ad AFTER DELETE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title, description, tags) VALUES('delete', old.rowid, old.title, old.description, old.tags); END;
		CREATE TRIGGER documents_au AFTER UPDATE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title, description, tags) VALUES('delete', old.rowid, old.title, old.description, old.tags);
			INSERT INTO documents_fts(rowid, title, description, tags) VALUES(new.rowid, new.title, new.description, new.tags); END;
		INSERT INTO documents_fts(documents_fts) VALUES('rebuild');`},
		{Version: 3, SQL: `
 CREATE TABLE document_shares (
   document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
   household_id TEXT NOT NULL, user_id TEXT NOT NULL, member_id TEXT NOT NULL,
   created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
   PRIMARY KEY(document_id,user_id));
 CREATE INDEX document_shares_recipient ON document_shares(household_id,member_id,document_id);`}}
}

type Document struct {
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
		{Method: http.MethodGet, Path: "/", Permission: Read, Handler: m.list},
		{Method: http.MethodPost, Path: "/", Permission: Write, Handler: m.upload},
		{Method: http.MethodGet, Path: "/{id}", Permission: Read, Handler: m.detail},
		{Method: http.MethodPatch, Path: "/{id}", Permission: Write, Handler: m.update},
		{Method: http.MethodGet, Path: "/{id}/download", Permission: Read, Handler: m.download},
		{Method: http.MethodGet, Path: "/{id}/shares", Permission: Read, Handler: m.shares},
		{Method: http.MethodPut, Path: "/{id}/shares/{user}", Permission: Share, Handler: m.share},
		{Method: http.MethodDelete, Path: "/{id}/shares/{user}", Permission: Share, Handler: m.unshare},
	}
}

func (m *Module) SearchPermission() string { return Read }

const columns = `d.id, d.title, d.category, d.created_at, d.description, d.tags, d.original_filename, d.media_type, d.size_bytes, d.sha256, (d.file_key != ''), d.owner_id, d.file_key`

func scanDocument(row interface{ Scan(...any) error }) (Document, error) {
	var d Document
	var tags string
	err := row.Scan(&d.ID, &d.Title, &d.Category, &d.CreatedAt, &d.Description, &tags, &d.Filename, &d.MediaType, &d.Size, &d.SHA256, &d.HasFile, &d.OwnerID, &d.fileKey)
	if err == nil {
		err = json.Unmarshal([]byte(tags), &d.Tags)
	}
	return d, err
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, ok := access.Current(req.Context())
	if !ok || !access.Allows(req.Context(), Read) {
		modules.Authorize(w, req, Read)
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
	if len(query) > 200 || (category != "" && !categories[category]) {
		fail(w, 400, "invalid_query")
		return
	}
	statement := "SELECT " + columns + " FROM documents d "
	if query != "" {
		statement += "JOIN documents_fts ON documents_fts.rowid = d.rowid "
	}
	statement += "WHERE " + readable + " "
	args := readArgs(p)
	if query != "" {
		statement += "AND documents_fts MATCH ? "
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
		modules.JSON(w, 500, map[string]string{"error": "documents_unavailable"})
		return
	}
	defer rows.Close()
	result := []Document{}
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			modules.JSON(w, 500, map[string]string{"error": "documents_unavailable"})
			return
		}
		result = append(result, d)
	}
	if rows.Err() != nil {
		modules.JSON(w, 500, map[string]string{"error": "documents_unavailable"})
		return
	}
	modules.JSON(w, 200, result)
}

func (m *Module) Search(ctx context.Context, query string) ([]modules.SearchResult, error) {
	result := []modules.SearchResult{}
	p, ok := access.Current(ctx)
	if !ok || !access.Allows(ctx, Read) || strings.TrimSpace(query) == "" {
		return result, nil
	}
	// Quoting makes input literal FTS text rather than exposing the FTS query language.
	query = literalQuery(query)
	rows, err := m.db.QueryContext(ctx, `SELECT d.id, d.title FROM documents_fts
		JOIN documents d ON d.rowid = documents_fts.rowid
		WHERE documents_fts MATCH ? AND `+readable+`
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
		match.Module = "documents"
		match.Path = "/documents/" + match.ID
		result = append(result, match)
	}
	return result, rows.Err()
}

func literalQuery(query string) string { return `"` + strings.ReplaceAll(query, `"`, `""`) + `"` }

// Ownership and explicit recipient grants always stay within the session household.
const readable = `d.household_id=? AND (d.owner_id=? OR EXISTS(
 SELECT 1 FROM document_shares s WHERE s.document_id=d.id AND s.household_id=d.household_id AND s.member_id=? AND s.user_id=?))`

func readArgs(p access.Principal) []any {
	return []any{p.HouseholdID, p.UserID, p.MembershipID, p.UserID}
}
