package collections

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type recipient struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Access   string `json:"access"`
	memberID string
}

type ListInput struct {
	Query, Category string
	Offset          int
}

func (m *Module) List(ctx context.Context, in ListInput) ([]Item, error) {
	p, ok := access.Current(ctx)
	if !ok {
		return nil, fault.New(fault.Unauthenticated, "authentication_required")
	}
	if !access.Allows(ctx, m.permission("read")) {
		return nil, fault.New(fault.Denied, "permission_denied")
	}
	in.Query = strings.TrimSpace(in.Query)
	if len(in.Query) > 200 || in.Offset < 0 || in.Offset > 1000000 || (in.Category != "" && !m.config.category(in.Category)) {
		return nil, fault.New(fault.Invalid, "invalid_query")
	}
	return m.repository.list(ctx, p, in)
}
func (m *Module) Lookup(ctx context.Context, id string, owner bool) (Item, string, error) {
	p, ok := access.Current(ctx)
	if !ok || !access.Allows(ctx, m.permission("read")) {
		return Item{}, "", fault.New(fault.Denied, "permission_denied")
	}
	if !storageKey.MatchString(id) {
		return Item{}, "", unitofwork.ErrNotFound
	}
	d, err := m.repository.lookup(ctx, p, id, owner)
	return d, d.fileKey, err
}
func (m *Module) Update(ctx context.Context, id string, raw []byte) (Item, error) {
	value, err := m.parseMetadata(ctx, raw)
	if errors.Is(err, ErrValidatorUnavailable) {
		return Item{}, fault.Retry(fault.Unavailable, "plugin_unavailable", 1)
	}
	if err != nil {
		return Item{}, fault.New(fault.Invalid, "invalid_metadata")
	}

	if _, _, err := m.Lookup(ctx, id, true); err != nil {
		return Item{}, err
	}
	p, _ := access.Current(ctx)
	tx, err := m.repository.Begin(ctx)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	if err := m.authorizeMutation(ctx, tx, "write"); err != nil {
		return Item{}, fault.New(fault.Denied, "permission_denied")
	}
	d, err := m.repository.update(ctx, tx, p, id, value)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}
func (m *Module) Shares(ctx context.Context, id string) ([]recipient, error) {
	if _, _, err := m.Lookup(ctx, id, true); err != nil {
		return nil, err
	}
	p, _ := access.Current(ctx)
	stored, err := m.repository.grants(ctx, id, p.HouseholdID)
	if err != nil {
		return nil, fault.New(fault.Internal, "items_unavailable")
	}
	result := []recipient{}
	for _, item := range stored {
		member, err := m.directory.Member(ctx, nil, p.HouseholdID, item.UserID)
		if errors.Is(err, unitofwork.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fault.New(fault.Unavailable, "members_unavailable")
		}
		if member.MembershipID == item.memberID {
			item.Name = member.Name
			item.Access = "read"
			result = append(result, item)
		}
	}
	return result, nil
}

func (m *Module) ChangeShare(ctx context.Context, id, target string, remove bool) error {
	if _, ok := access.Current(ctx); !ok {
		return fault.New(fault.Unauthenticated, "authentication_required")
	}
	if !access.Allows(ctx, m.permission("read")) {
		return fault.New(fault.Denied, "permission_denied")
	}
	if _, _, err := m.Lookup(ctx, id, true); err != nil {
		return err
	}
	p, _ := access.Current(ctx)

	if target == p.UserID {
		return fault.New(fault.Invalid, "invalid_recipient")
	}
	tx, err := m.repository.Begin(ctx)
	if err != nil {
		return fault.New(fault.Internal, "items_unavailable")
	}
	defer tx.Rollback()
	// Recheck capabilities and ownership under the same transaction as the grant.
	if err := m.authorizeMutation(ctx, tx, "share"); err != nil {
		return fault.New(fault.Denied, "permission_denied")
	}
	owns, err := m.repository.owns(ctx, tx, p, id)
	if err != nil {
		return fault.New(fault.Internal, "items_unavailable")
	}
	if !owns {
		return fault.New(fault.Missing, "item_not_found")
	}
	memberID := ""
	if !remove {
		member, lookupErr := m.directory.Member(ctx, tx, p.HouseholdID, target)
		if errors.Is(lookupErr, unitofwork.ErrNotFound) {
			return fault.New(fault.Missing, "member_not_found")
		}
		if lookupErr != nil {
			return fault.New(fault.Unavailable, "members_unavailable")
		}
		if !slices.Contains(member.Permissions, m.permission("read")) {
			return fault.New(fault.Conflict, "recipient_cannot_read")
		}
		memberID = member.MembershipID
	}
	err = m.repository.changeGrant(ctx, tx, p, id, target, memberID, remove)
	if err != nil {
		return fault.New(fault.Internal, "items_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.Internal, "items_unavailable")
	}
	return nil
}

func (m *Module) authorizeMutation(ctx context.Context, tx unitofwork.Transaction, capability string) error {
	p, ok := access.Current(ctx)
	if !ok {
		return errors.New("missing identity")
	}
	member, err := m.directory.Member(ctx, tx, p.HouseholdID, p.UserID)
	if err != nil {
		return err
	}
	if member.MembershipID != p.MembershipID || !slices.Contains(member.Permissions, m.permission("read")) || !slices.Contains(member.Permissions, m.permission(capability)) {
		return errors.New("revoked permission")
	}
	enabled, err := m.repository.enabled(ctx, tx, p.HouseholdID)
	if err != nil {
		return err
	}
	if !enabled {
		return errors.New("module disabled")
	}
	return nil
}

func (m *Module) Detail(ctx context.Context, id string) (Item, error) {
	d, _, err := m.Lookup(ctx, id, false)
	if err != nil {
		return Item{}, err
	}
	if m.directory != nil {
		p, _ := access.Current(ctx)
		owner, err := m.directory.Member(ctx, nil, p.HouseholdID, d.OwnerID)
		if err == nil {
			d.OwnerName = owner.Name
		} else if !errors.Is(err, unitofwork.ErrNotFound) {
			return Item{}, fault.New(fault.Unavailable, "members_unavailable")
		}
	}
	return d, nil
}
