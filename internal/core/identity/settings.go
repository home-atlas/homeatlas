package identity

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

var errAuthenticationRequired = errors.New("authentication required")
var errManagementDenied = errors.New("household management denied")

func validTimezone(value string) bool {
	if value == "" || value == "Local" || len(value) > 100 {
		return false
	}
	_, err := time.LoadLocation(value)
	return err == nil
}

func (s *Service) managementActor(ctx context.Context, tx *sql.Tx) (access.Member, error) {
	p, ok := access.Current(ctx)
	if !ok {
		return access.Member{}, errAuthenticationRequired
	}
	m, err := s.Member(ctx, tx, p.HouseholdID, p.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return m, errAuthenticationRequired
	}
	if err != nil {
		return m, err
	}
	if m.MembershipID != p.MembershipID {
		return m, errAuthenticationRequired
	}
	if !manager(m) {
		return m, errManagementDenied
	}
	return m, nil
}

func (s *Service) managementError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAuthenticationRequired), errors.Is(err, modules.ErrHouseholdRequired):
		fail(w, 401, "authentication_required")
	case errors.Is(err, errManagementDenied):
		fail(w, 403, "permission_denied")
	case errors.Is(err, modules.ErrUnknownModule):
		fail(w, 404, "module_not_found")
	case errors.Is(err, modules.ErrUnavailable):
		fail(w, 409, "module_unavailable")
	case errors.Is(err, modules.ErrDependency):
		fail(w, 409, "module_dependency_conflict")
	default:
		fail(w, 503, "management_unavailable")
	}
}

func (s *Service) householdSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := access.Current(r.Context())
	if !ok {
		fail(w, 401, "authentication_required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.managementError(w, err)
		return
	}
	defer tx.Rollback()
	m, err := s.Member(r.Context(), tx, p.HouseholdID, p.UserID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && m.MembershipID != p.MembershipID {
		s.managementError(w, errAuthenticationRequired)
		return
	}
	if err != nil {
		s.managementError(w, err)
		return
	}
	var h Household
	if err := tx.QueryRowContext(r.Context(), `SELECT id,name,timezone FROM identity_households WHERE id=?`, p.HouseholdID).Scan(&h.ID, &h.Name, &h.Timezone); err != nil {
		s.managementError(w, err)
		return
	}
	modules.JSON(w, 200, map[string]any{"household": h, "canManage": manager(m)})
}

func (s *Service) updateHouseholdSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validText(input.Name, 100) || !validTimezone(input.Timezone) {
		fail(w, 400, "invalid_household_settings")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.managementError(w, err)
		return
	}
	defer tx.Rollback()
	if _, ok := s.actor(w, r, tx); !ok {
		return
	}
	p, _ := access.Current(r.Context())
	if _, err := tx.ExecContext(r.Context(), `UPDATE identity_households SET name=?,timezone=? WHERE id=?`, input.Name, input.Timezone, p.HouseholdID); err != nil {
		s.managementError(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.managementError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

func (s *Service) updateModule(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		fail(w, 400, "invalid_module_settings")
		return
	}
	err := s.modules.SetHouseholdEnabled(r.Context(), r.PathValue("id"), *input.Enabled, func(tx *sql.Tx) error {
		_, err := s.managementActor(r.Context(), tx)
		return err
	})
	if err != nil {
		s.managementError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}
