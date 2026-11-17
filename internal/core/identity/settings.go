package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
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

func (s *Application) managementActor(ctx context.Context, tx unitofwork.Transaction) (access.Member, error) {
	p, ok := access.Current(ctx)
	if !ok {
		return access.Member{}, errAuthenticationRequired
	}
	m, err := s.Member(ctx, tx, p.HouseholdID, p.UserID)
	if errors.Is(err, unitofwork.ErrNotFound) {
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

func managementFailure(err error) error {
	switch {
	case errors.Is(err, errAuthenticationRequired), errors.Is(err, modules.ErrHouseholdRequired):
		return fault.New(fault.Unauthenticated, "authentication_required")
	case errors.Is(err, errManagementDenied):
		return fault.New(fault.Denied, "permission_denied")
	case errors.Is(err, modules.ErrUnknownModule):
		return fault.New(fault.Missing, "module_not_found")
	case errors.Is(err, modules.ErrUnavailable):
		return fault.New(fault.Conflict, "module_unavailable")
	case errors.Is(err, modules.ErrDependency):
		return fault.New(fault.Conflict, "module_dependency_conflict")
	default:
		return fault.New(fault.Unavailable, "management_unavailable")
	}
}

func (s *Application) HouseholdSettings(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	p, ok := access.Current(ctx)
	if !ok {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, managementFailure(err)
	}
	defer tx.Rollback()
	m, err := s.Member(ctx, tx, p.HouseholdID, p.UserID)
	if errors.Is(err, unitofwork.ErrNotFound) || err == nil && m.MembershipID != p.MembershipID {
		return result, managementFailure(errAuthenticationRequired)
	}
	if err != nil {
		return result, managementFailure(err)
	}
	var h Household
	if err := s.store.readHouseholdSettings(ctx, tx, p.HouseholdID, &h.ID, &h.Name, &h.Timezone); err != nil {
		return result, managementFailure(err)
	}
	result.Data = map[string]any{"household": h, "canManage": manager(m)}

	return result, nil
}

func (s *Application) UpdateHouseholdSettings(ctx context.Context, client Client, input UpdateHouseholdSettingsInput) (Result, error) {
	result := Result{}

	input.Name = strings.TrimSpace(input.Name)
	if !validText(input.Name, 100) || !validTimezone(input.Timezone) {
		return result, fault.New(fault.Invalid, "invalid_household_settings")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, managementFailure(err)
	}
	defer tx.Rollback()
	if _, err := s.actor(ctx, tx); err != nil {
		return result, err
	}
	p, _ := access.Current(ctx)
	if _, err := s.store.saveHouseholdSettings(ctx, tx, input.Name, input.Timezone, p.HouseholdID); err != nil {
		return result, managementFailure(err)
	}
	if err := tx.Commit(); err != nil {
		return result, managementFailure(err)
	}

	return result, nil
}

func (s *Application) UpdateModule(ctx context.Context, client Client, target string, input UpdateModuleInput) (Result, error) {
	result := Result{}

	if input.Enabled == nil {
		return result, fault.New(fault.Invalid, "invalid_module_settings")
	}
	err := s.modules.SetHouseholdEnabled(ctx, target, *input.Enabled, func(tx unitofwork.Transaction) error {
		_, err := s.managementActor(ctx, tx)
		return err
	})
	if err != nil {
		return result, managementFailure(err)
	}

	return result, nil
}
