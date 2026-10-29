package documents

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

type recipient struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Access   string `json:"access"`
	memberID string
}

func (m *Module) shares(w http.ResponseWriter, r *http.Request) {
	if _, _, err := m.owned(r); err != nil {
		documentError(w, err)
		return
	}
	p, _ := access.Current(r.Context())
	rows, err := m.db.QueryContext(r.Context(), `SELECT user_id,member_id FROM document_shares WHERE document_id=? AND household_id=? ORDER BY created_at,user_id`, r.PathValue("id"), p.HouseholdID)
	if err != nil {
		fail(w, 500, "documents_unavailable")
		return
	}
	stored := []recipient{}
	for rows.Next() {
		var item recipient
		if err := rows.Scan(&item.UserID, &item.memberID); err != nil {
			rows.Close()
			fail(w, 500, "documents_unavailable")
			return
		}
		stored = append(stored, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "documents_unavailable")
		return
	}
	result := []recipient{}
	for _, item := range stored {
		member, err := m.directory.Member(r.Context(), nil, p.HouseholdID, item.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			fail(w, 503, "members_unavailable")
			return
		}
		if member.MembershipID == item.memberID {
			item.Name = member.Name
			item.Access = "read"
			result = append(result, item)
		}
	}
	modules.JSON(w, 200, result)
}
func (m *Module) changeShare(w http.ResponseWriter, r *http.Request, remove bool) {
	if !modules.Authorize(w, r, Read) {
		return
	}
	if _, _, err := m.owned(r); err != nil {
		documentError(w, err)
		return
	}
	p, _ := access.Current(r.Context())
	target := r.PathValue("user")
	if target == p.UserID {
		fail(w, 400, "invalid_recipient")
		return
	}
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "documents_unavailable")
		return
	}
	defer tx.Rollback()
	// Recheck capabilities and ownership under the same transaction as the grant.
	actor, err := m.directory.Member(r.Context(), tx, p.HouseholdID, p.UserID)
	if err != nil || !slices.Contains(actor.Permissions, Share) || !slices.Contains(actor.Permissions, Read) {
		fail(w, 403, "permission_denied")
		return
	}
	var owns bool
	if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM documents WHERE id=? AND household_id=? AND owner_id=?)`, r.PathValue("id"), p.HouseholdID, p.UserID).Scan(&owns); err != nil {
		fail(w, 500, "documents_unavailable")
		return
	}
	if !owns {
		fail(w, 404, "document_not_found")
		return
	}
	if remove {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM document_shares WHERE document_id=? AND household_id=? AND user_id=?`, r.PathValue("id"), p.HouseholdID, target)
	} else {
		member, lookupErr := m.directory.Member(r.Context(), tx, p.HouseholdID, target)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			fail(w, 404, "member_not_found")
			return
		}
		if lookupErr != nil {
			fail(w, 503, "members_unavailable")
			return
		}
		if !slices.Contains(member.Permissions, Read) {
			fail(w, 409, "recipient_cannot_read")
			return
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO document_shares(document_id,household_id,user_id,member_id) VALUES(?,?,?,?) ON CONFLICT(document_id,user_id) DO UPDATE SET member_id=excluded.member_id`, r.PathValue("id"), p.HouseholdID, target, member.MembershipID)
	}
	if err != nil {
		fail(w, 500, "documents_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "documents_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}
func (m *Module) share(w http.ResponseWriter, r *http.Request)   { m.changeShare(w, r, false) }
func (m *Module) unshare(w http.ResponseWriter, r *http.Request) { m.changeShare(w, r, true) }
