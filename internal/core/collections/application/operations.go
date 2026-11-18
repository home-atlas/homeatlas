package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/collections/domain"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type ListInput struct {
	Query, Category string
	Offset          int
}

func (m *Module) List(ctx context.Context, in ListInput) ([]Item, error) {
	p, ok := access.Current(ctx)
	if !ok {
		return nil, fault.New(fault.Unauthenticated, "authentication_required")
	}
	if !access.Allows(ctx, m.Permission("read")) {
		return nil, fault.New(fault.Denied, "permission_denied")
	}
	in.Query = strings.TrimSpace(in.Query)
	if len(in.Query) > 200 || in.Offset < 0 || in.Offset > 1000000 || (in.Category != "" && !m.config.Category(in.Category)) {
		return nil, fault.New(fault.Invalid, "invalid_query")
	}
	return m.repository.List(ctx, p, in)
}
func (m *Module) Lookup(ctx context.Context, id string, owner bool) (Item, string, error) {
	p, ok := access.Current(ctx)
	if !ok || !access.Allows(ctx, m.Permission("read")) {
		return Item{}, "", fault.New(fault.Denied, "permission_denied")
	}
	if !domain.StorageKey.MatchString(id) {
		return Item{}, "", unitofwork.ErrNotFound
	}
	d, err := m.repository.Lookup(ctx, p, id, owner)
	return d, d.FileKey, err
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
	d, err := m.repository.Update(ctx, tx, p, id, value)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}
func (m *Module) Shares(ctx context.Context, id string) ([]Recipient, error) {
	if _, _, err := m.Lookup(ctx, id, true); err != nil {
		return nil, err
	}
	p, _ := access.Current(ctx)
	stored, err := m.repository.Grants(ctx, id, p.HouseholdID)
	if err != nil {
		return nil, fault.New(fault.Internal, "items_unavailable")
	}
	result := []Recipient{}
	for _, item := range stored {
		member, err := m.directory.Member(ctx, nil, p.HouseholdID, item.UserID)
		if errors.Is(err, unitofwork.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fault.New(fault.Unavailable, "members_unavailable")
		}
		if member.MembershipID == item.MemberID {
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
	if !access.Allows(ctx, m.Permission("read")) {
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
	owns, err := m.repository.Owns(ctx, tx, p, id)
	if err != nil {
		return fault.New(fault.Internal, "items_unavailable")
	}
	if !owns {
		return fault.New(fault.Missing, "item_not_found")
	}
	MemberID := ""
	if !remove {
		member, lookupErr := m.directory.Member(ctx, tx, p.HouseholdID, target)
		if errors.Is(lookupErr, unitofwork.ErrNotFound) {
			return fault.New(fault.Missing, "member_not_found")
		}
		if lookupErr != nil {
			return fault.New(fault.Unavailable, "members_unavailable")
		}
		if !slices.Contains(member.Permissions, m.Permission("read")) {
			return fault.New(fault.Conflict, "recipient_cannot_read")
		}
		MemberID = member.MembershipID
	}
	err = m.repository.ChangeGrant(ctx, tx, p, id, target, MemberID, remove)
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
	if member.MembershipID != p.MembershipID || !slices.Contains(member.Permissions, m.Permission("read")) || !slices.Contains(member.Permissions, m.Permission(capability)) {
		return errors.New("revoked permission")
	}
	enabled, err := m.repository.Enabled(ctx, tx, p.HouseholdID)
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

func (m *Module) parseMetadata(ctx context.Context, data []byte) (Metadata, error) {
	var value Metadata
	if len(data) > metadataLimit {
		return value, errors.New("metadata too large")
	}
	normalized, err := m.config.Validate(ctx, data)
	if err != nil {
		return value, fmt.Errorf("%w: %v", ErrValidatorUnavailable, err)
	}
	if len(normalized) > metadataLimit {
		return value, errors.New("invalid plugin metadata")
	}
	data = normalized
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return value, errors.New("trailing data")
	}
	value.Title = strings.TrimSpace(value.Title)
	value.Description = strings.TrimSpace(value.Description)
	if !domain.Text(value.Title, m.config.TitleMaxLength, false) || value.Title == "" || !m.config.Category(value.Category) || !domain.Text(value.Description, m.config.DescriptionMaxLength, true) || len(value.Tags) > m.config.MaxTags {
		return value, errors.New("invalid metadata")
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, tag := range value.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !domain.Text(tag, m.config.TagMaxLength, false) {
			return value, errors.New("invalid tag")
		}
		key := strings.ToLower(tag)
		if !seen[key] {
			tags = append(tags, tag)
			seen[key] = true
		}
	}
	value.Tags = tags
	return value, nil
}
