package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

const Manage = "household.manage"

func (s *Service) mountMembers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/household/members", s.members)
	mux.HandleFunc("PATCH /api/household/members/{user}", s.updateMember)
	mux.HandleFunc("DELETE /api/household/members/{user}", s.removeMember)
	mux.HandleFunc("GET /api/household/invitations", s.invitations)
	mux.HandleFunc("POST /api/household/invitations", s.invite)
	mux.HandleFunc("DELETE /api/household/invitations/{id}", s.revokeInvitation)
	mux.HandleFunc("POST /api/invitations/inspect", s.inspectInvitation)
	mux.HandleFunc("POST /api/invitations/accept", s.acceptInvitation)
}

func (s *Service) Member(ctx context.Context, tx *sql.Tx, household, user string) (access.Member, error) {
	var q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	} = s.db
	if tx != nil {
		q = tx
	}
	m := access.Member{Permissions: []string{}}
	err := q.QueryRowContext(ctx, `SELECT u.id,mi.id,u.name,COALESCE(u.email,''),m.role,COALESCE(u.username,'u-'||lower(u.id)),u.assisted=1 AND u.password_hash='!' FROM identity_memberships m
 JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id
 JOIN identity_users u ON u.id=m.user_id WHERE m.household_id=? AND m.user_id=?`, household, user).Scan(&m.ID, &m.MembershipID, &m.Name, &m.Email, &m.Role, &m.Username, &m.Assisted)
	if err != nil {
		return m, err
	}
	rows, err := q.QueryContext(ctx, `SELECT capability FROM identity_permissions WHERE household_id=? AND user_id=? ORDER BY capability`, household, user)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return m, err
		}
		m.Permissions = append(m.Permissions, permission)
	}
	return m, rows.Err()
}

func manager(m access.Member) bool {
	return (m.Role == "owner" || m.Role == "admin") && slices.Contains(m.Permissions, Manage)
}
func (s *Service) actor(w http.ResponseWriter, r *http.Request, tx *sql.Tx) (access.Member, bool) {
	m, err := s.managementActor(r.Context(), tx)
	if err != nil {
		s.managementError(w, err)
		return m, false
	}
	return m, true
}

func (s *Service) members(w http.ResponseWriter, r *http.Request) {
	p, ok := access.Current(r.Context())
	if !ok {
		fail(w, 401, "authentication_required")
		return
	}
	actor, err := s.Member(r.Context(), nil, p.HouseholdID, p.UserID)
	if err != nil {
		fail(w, 503, "members_unavailable")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT user_id FROM identity_memberships WHERE household_id=? ORDER BY role,user_id`, p.HouseholdID)
	if err != nil {
		fail(w, 503, "members_unavailable")
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			fail(w, 503, "members_unavailable")
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 503, "members_unavailable")
		return
	}
	members := []access.Member{}
	for _, id := range ids {
		m, err := s.Member(r.Context(), nil, p.HouseholdID, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			fail(w, 503, "members_unavailable")
			return
		}
		if !manager(actor) && id != p.UserID {
			m.Email = ""
		}
		members = append(members, m)
	}
	roles := []string{"member", "restricted", "guest"}
	if actor.Role == "owner" {
		roles = append([]string{"admin"}, roles...)
	}
	capabilities := []string{}
	for _, cap := range s.capabilities {
		if slices.Contains(actor.Permissions, cap) {
			capabilities = append(capabilities, cap)
		}
	}
	modules.JSON(w, 200, map[string]any{"members": members, "canManage": manager(actor), "roles": roles, "capabilities": capabilities})
}

type grantInput struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

func (s *Service) grant(actor access.Member, role string, permissions []string) ([]string, bool) {
	if !manager(actor) || !slices.Contains([]string{"admin", "member", "restricted", "guest"}, role) || (role == "admin" && actor.Role != "owner") {
		return nil, false
	}
	if len(permissions) > len(s.capabilities) {
		return nil, false
	}
	result := []string{}
	for _, cap := range permissions {
		if !slices.Contains(s.capabilities, cap) || !slices.Contains(actor.Permissions, cap) || slices.Contains(result, cap) {
			return nil, false
		}
		if (role == "restricted" || role == "guest") && !strings.HasSuffix(cap, ".read") {
			return nil, false
		}
		result = append(result, cap)
	}
	for _, cap := range result {
		if strings.HasSuffix(cap, ".write") || strings.HasSuffix(cap, ".share") {
			prefix := cap[:strings.LastIndex(cap, ".")]
			if !slices.Contains(result, prefix+".read") {
				return nil, false
			}
		}
	}
	if role == "admin" {
		result = append(result, Manage)
	}
	return result, true
}
func editable(actor, target access.Member) bool {
	return target.ID != actor.ID && target.Role != "owner" && (actor.Role == "owner" || target.Role != "admin")
}
func setPermissions(ctx context.Context, tx *sql.Tx, household, user string, permissions []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM identity_permissions WHERE household_id=? AND user_id=?`, household, user); err != nil {
		return err
	}
	for _, cap := range permissions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO identity_permissions VALUES(?,?,?)`, user, household, cap); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) changeMember(w http.ResponseWriter, r *http.Request, remove bool) {
	var input grantInput
	if !remove && !decode(w, r, &input) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "members_unavailable")
		return
	}
	defer tx.Rollback()
	actor, ok := s.actor(w, r, tx)
	if !ok {
		return
	}
	p, _ := access.Current(r.Context())
	target, err := s.Member(r.Context(), tx, p.HouseholdID, r.PathValue("user"))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "member_not_found")
		return
	}
	if err != nil {
		fail(w, 503, "members_unavailable")
		return
	}
	if !editable(actor, target) {
		fail(w, 403, "protected_member")
		return
	}
	if remove {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM identity_memberships WHERE household_id=? AND user_id=?`, p.HouseholdID, target.ID)
	} else {
		permissions, ok := s.grant(actor, input.Role, input.Permissions)
		if !ok {
			fail(w, 403, "invalid_grant")
			return
		}
		if target.Assisted {
			allowed := pinRole(input.Role)
			for _, cap := range permissions {
				if strings.HasPrefix(cap, "household.") {
					allowed = false
				}
			}
			if !allowed {
				fail(w, 403, "assisted_admin_not_allowed")
				return
			}
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE identity_memberships SET role=? WHERE household_id=? AND user_id=?`, input.Role, p.HouseholdID, target.ID)
		if err == nil {
			err = setPermissions(r.Context(), tx, p.HouseholdID, target.ID, permissions)
		}
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE identity_invitations SET revoked_at=? WHERE household_id=? AND accepted_at=0 AND revoked_at=0 AND (issuer_id=? OR email=?)`, time.Now().Unix(), p.HouseholdID, target.ID, target.Email)
	}
	if err != nil {
		fail(w, 503, "members_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "members_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}
func (s *Service) updateMember(w http.ResponseWriter, r *http.Request) { s.changeMember(w, r, false) }
func (s *Service) removeMember(w http.ResponseWriter, r *http.Request) { s.changeMember(w, r, true) }

func normalizedPermissions(permissions []string) []string {
	result := []string{}
	for _, cap := range permissions {
		if cap != Manage {
			result = append(result, strings.TrimSpace(cap))
		}
	}
	return result
}
