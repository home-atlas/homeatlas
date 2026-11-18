package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	modules "github.com/home-atlas/homeatlas/internal/core/modules/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository struct{ db *scopedDB }

func (s *Repository) Begin(ctx context.Context) (unitofwork.Transaction, error) {
	return s.db.BeginTx(ctx, nil)
}
func (s *Repository) transaction(tx unitofwork.Transaction) (*scopedTx, error) {
	raw, ok := tx.(*scopedTx)
	if !ok {
		return nil, errors.New("invalid collection transaction")
	}
	return raw, nil
}

const columns = `d.id, d.title, d.category, d.created_at, d.description, d.tags, d.original_filename, d.media_type, d.size_bytes, d.sha256, (d.file_key != ''), d.owner_id, d.file_key`

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var d Item
	var tags string
	err := row.Scan(&d.ID, &d.Title, &d.Category, &d.CreatedAt, &d.Description, &tags, &d.Filename, &d.MediaType, &d.Size, &d.SHA256, &d.HasFile, &d.OwnerID, &d.FileKey)
	if err == nil {
		err = json.Unmarshal([]byte(tags), &d.Tags)
	}
	return d, err
}

// Ownership and explicit recipient grants always stay within the session household.
const readable = `d.household_id=? AND (d.owner_id=? OR EXISTS(
 SELECT 1 FROM collection_grants s WHERE s.item_id=d.id AND s.household_id=d.household_id AND s.member_id=? AND s.user_id=?))`

func readArgs(p access.Principal) []any {
	return []any{p.HouseholdID, p.UserID, p.MembershipID, p.UserID}
}

func (s *Repository) List(ctx context.Context, p access.Principal, in ListInput) ([]Item, error) {
	statement := "SELECT " + columns + " FROM collection_items d "
	if in.Query != "" {
		statement += "JOIN collection_fts ON collection_fts.rowid = d.rowid "
	}
	statement += "WHERE " + readable + " "
	args := readArgs(p)
	if in.Query != "" {
		statement += "AND collection_fts MATCH ? "
		args = append(args, literalQuery(in.Query))
	}
	if in.Category != "" {
		statement += "AND d.category = ? "
		args = append(args, in.Category)
	}
	statement += "ORDER BY d.created_at DESC, d.id LIMIT 50 OFFSET ?"
	args = append(args, in.Offset)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Item{}
	for rows.Next() {
		d, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func (s *Repository) Search(ctx context.Context, p access.Principal, query string) ([]modules.SearchResult, error) {
	query = literalQuery(query)
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.title FROM collection_fts
 JOIN collection_items d ON d.rowid = collection_fts.rowid
 WHERE collection_fts MATCH ? AND `+readable+` ORDER BY rank LIMIT 50`, append([]any{query}, readArgs(p)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []modules.SearchResult{}
	for rows.Next() {
		var v modules.SearchResult
		if err := rows.Scan(&v.ID, &v.Title); err != nil {
			return nil, err
		}
		v.Module = s.db.config.ID
		v.Path = "/" + v.Module + "/" + v.ID
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Repository) Lookup(ctx context.Context, p access.Principal, id string, owner bool) (Item, error) {
	scope := readable
	args := readArgs(p)
	if owner {
		scope = "d.household_id=? AND d.owner_id=?"
		args = []any{p.HouseholdID, p.UserID}
	}
	d, err := scanItem(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM collection_items d WHERE d.id=? AND "+scope, append([]any{id}, args...)...))
	return d, database.Normalize(err)
}
func (s *Repository) Grants(ctx context.Context, id, home string) ([]Recipient, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,member_id FROM collection_grants WHERE item_id=? AND household_id=? ORDER BY created_at,user_id`, id, home)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recipient{}
	for rows.Next() {
		var v Recipient
		if err := rows.Scan(&v.UserID, &v.MemberID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Repository) Owns(ctx context.Context, tx unitofwork.Transaction, p access.Principal, id string) (bool, error) {
	q, err := s.transaction(tx)
	if err != nil {
		return false, err
	}
	var Owns bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM collection_items WHERE id=? AND household_id=? AND owner_id=?)`, id, p.HouseholdID, p.UserID).Scan(&Owns)
	return Owns, err
}
func (s *Repository) ChangeGrant(ctx context.Context, tx unitofwork.Transaction, p access.Principal, id, user, member string, remove bool) error {
	q, err := s.transaction(tx)
	if err != nil {
		return err
	}
	if remove {
		_, err = q.ExecContext(ctx, `DELETE FROM collection_grants WHERE item_id=? AND household_id=? AND user_id=?`, id, p.HouseholdID, user)
	} else {
		_, err = q.ExecContext(ctx, `INSERT INTO collection_grants(item_id,household_id,user_id,member_id) VALUES(?,?,?,?) ON CONFLICT(item_id,user_id) DO UPDATE SET member_id=excluded.member_id`, id, p.HouseholdID, user, member)
	}
	return err
}
func (s *Repository) Update(ctx context.Context, tx unitofwork.Transaction, p access.Principal, id string, value Metadata) (Item, error) {
	q, err := s.transaction(tx)
	if err != nil {
		return Item{}, err
	}
	tags, _ := json.Marshal(value.Tags)
	d, err := scanItem(q.QueryRowContext(ctx, `UPDATE collection_items SET title=?,category=?,description=?,tags=? WHERE id=? AND household_id=? AND owner_id=? RETURNING `+strings.ReplaceAll(columns, "d.", ""), value.Title, value.Category, value.Description, string(tags), id, p.HouseholdID, p.UserID))
	return d, database.Normalize(err)
}

func (s *Repository) Enabled(ctx context.Context, tx unitofwork.Transaction, home string) (bool, error) {
	q, err := s.transaction(tx)
	if err != nil {
		return false, err
	}
	var Enabled bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM module_states m WHERE m.id=? AND m.enabled=1
 AND NOT EXISTS(SELECT 1 FROM household_module_states h WHERE h.module_id=m.id AND h.household_id=? AND h.enabled=0))`, s.db.config.ID, home).Scan(&Enabled)
	return Enabled, err
}

func literalQuery(query string) string { return `"` + strings.ReplaceAll(query, `"`, `""`) + `"` }
