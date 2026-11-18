package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

func (s *Application) Member(ctx context.Context, tx unitofwork.Transaction, HouseholdID, user string) (access.Member, error) {
	return s.store.ReadMember(ctx, tx, HouseholdID, user)
}

func (s *Application) actor(ctx context.Context, tx unitofwork.Transaction) (access.Member, error) {
	m, err := s.managementActor(ctx, tx)
	if err != nil {
		return m, managementFailure(err)
	}
	return m, nil
}

func (s *Application) Members(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	p, ok := access.Current(ctx)
	if !ok {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	actor, err := s.Member(ctx, nil, p.HouseholdID, p.UserID)
	if err != nil {
		return result, fault.New(fault.Unavailable, "members_unavailable")
	}
	ids, err := s.store.ReadMemberIDs(ctx, p.HouseholdID)
	if err != nil {
		return result, fault.New(fault.Unavailable, "members_unavailable")
	}
	members := []access.Member{}
	for _, id := range ids {
		m, err := s.Member(ctx, nil, p.HouseholdID, id)
		if errors.Is(err, unitofwork.ErrNotFound) {
			continue
		}
		if err != nil {
			return result, fault.New(fault.Unavailable, "members_unavailable")
		}
		if !domain.Manager(actor) && id != p.UserID {
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
	result.Data = map[string]any{"members": members, "canManage": domain.Manager(actor), "roles": roles, "capabilities": capabilities}

	return result, nil
}

type GrantInput struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

func (s *Application) grant(actor access.Member, role string, permissions []string) ([]string, bool) {
	if !domain.Manager(actor) || !slices.Contains([]string{"admin", "member", "restricted", "guest"}, role) || (role == "admin" && actor.Role != "owner") {
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

func (s *Application) setPermissions(ctx context.Context, tx unitofwork.Transaction, HouseholdID, user string, permissions []string) error {
	if _, err := s.store.ClearMemberCapabilities(ctx, tx, HouseholdID, user); err != nil {
		return err
	}
	for _, cap := range permissions {
		if _, err := s.store.InsertMemberCapability(ctx, tx, user, HouseholdID, cap); err != nil {
			return err
		}
	}
	return nil
}
func (s *Application) ChangeMember(ctx context.Context, client Client, targetID string, input GrantInput, remove bool) (Result, error) {
	result := Result{}

	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "members_unavailable")
	}
	defer tx.Rollback()
	actor, err := s.actor(ctx, tx)
	if err != nil {
		return result, err
	}
	p, _ := access.Current(ctx)
	target, err := s.Member(ctx, tx, p.HouseholdID, targetID)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return result, fault.New(fault.Missing, "member_not_found")
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "members_unavailable")
	}
	if !domain.Editable(actor, target) {
		return result, fault.New(fault.Denied, "protected_member")
	}
	if remove {
		_, err = s.store.DeleteMembership(ctx, tx, p.HouseholdID, target.ID)
	} else {
		permissions, ok := s.grant(actor, input.Role, input.Permissions)
		if !ok {
			return result, fault.New(fault.Denied, "invalid_grant")
		}
		if target.Assisted {
			allowed := domain.PinRole(input.Role)
			for _, cap := range permissions {
				if strings.HasPrefix(cap, "household.") {
					allowed = false
				}
			}
			if !allowed {
				return result, fault.New(fault.Denied, "assisted_admin_not_allowed")
			}
		}
		_, err = s.store.SaveMembershipRole(ctx, tx, input.Role, p.HouseholdID, target.ID)
		if err == nil {
			err = s.setPermissions(ctx, tx, p.HouseholdID, target.ID, permissions)
		}
	}
	if err == nil {
		_, err = s.store.RevokeMemberInvitations(ctx, tx, time.Now().Unix(), p.HouseholdID, target.ID, target.Email)
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "members_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "members_unavailable")
	}

	return result, nil
}
