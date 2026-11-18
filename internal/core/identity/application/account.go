package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type UpdateProfileInput struct {
	Name string `json:"name"`
}

func accountFailure(err error) error {
	if errors.Is(err, unitofwork.ErrNotFound) {
		return fault.New(fault.Unauthenticated, "authentication_required")
	}
	return fault.New(fault.Unavailable, "account_unavailable")
}
func (s *Application) ownAccount(ctx context.Context, tx unitofwork.Transaction, client Client) (domain.AccountProfile, []byte, error) {
	p, ok := access.Current(ctx)
	if !ok || !client.HasSession {
		return domain.AccountProfile{}, nil, accountFailure(unitofwork.ErrNotFound)
	}
	hash := sha256.Sum256([]byte(client.SessionToken))
	now := time.Now().Unix()
	v, err := s.store.ReadOwnAccount(ctx, tx, hash[:], p.UserID, p.HouseholdID, p.MembershipID, now, now-int64(domain.SessionIdle.Seconds()))
	if err != nil {
		return v, nil, accountFailure(err)
	}
	return v, hash[:], nil
}

func (s *Application) Profile(ctx context.Context, client Client) (Result, error) {
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return Result{}, accountFailure(err)
	}
	defer tx.Rollback()
	v, _, err := s.ownAccount(ctx, tx, client)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: v}, nil
}

func (s *Application) UpdateProfile(ctx context.Context, client Client, input UpdateProfileInput) (Result, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !domain.ValidText(input.Name, 80) {
		return Result{}, fault.New(fault.Invalid, "invalid_profile")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return Result{}, accountFailure(err)
	}
	defer tx.Rollback()
	v, _, err := s.ownAccount(ctx, tx, client)
	if err != nil {
		return Result{}, err
	}
	if err = s.store.SaveOwnName(ctx, tx, v.User.ID, input.Name); err != nil {
		return Result{}, accountFailure(err)
	}
	if err = tx.Commit(); err != nil {
		return Result{}, accountFailure(err)
	}
	v.User.Name = input.Name
	return Result{Data: v}, nil
}

func (s *Application) AccountSessions(ctx context.Context, client Client) (Result, error) {
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return Result{}, accountFailure(err)
	}
	defer tx.Rollback()
	_, hash, err := s.ownAccount(ctx, tx, client)
	if err != nil {
		return Result{}, err
	}
	p, _ := access.Current(ctx)
	now := time.Now().Unix()
	items, err := s.store.ReadOwnSessions(ctx, tx, p.UserID, p.HouseholdID, hash, now, now-int64(domain.SessionIdle.Seconds()))
	if err != nil {
		return Result{}, accountFailure(err)
	}
	more := len(items) > 100
	if more {
		items = items[:100]
	}
	for i := range items {
		items[i].ID = domain.SessionHandle(items[i].TokenHash)
		items[i].Current = bytes.Equal(items[i].TokenHash, hash)
	}
	return Result{Data: struct {
		Sessions []domain.AccountSession `json:"sessions"`
		More     bool                    `json:"more"`
	}{items, more}}, nil
}

func (s *Application) RevokeAccountSession(ctx context.Context, client Client, id string, others bool) (Result, error) {
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return Result{}, accountFailure(err)
	}
	defer tx.Rollback()
	_, hash, err := s.ownAccount(ctx, tx, client)
	if err != nil {
		return Result{}, err
	}
	p, _ := access.Current(ctx)
	end := false
	if others {
		err = s.store.DeleteOtherOwnSessions(ctx, tx, p.UserID, p.HouseholdID, hash)
	} else {
		now := time.Now().Unix()
		items, readErr := s.store.ReadOwnSessions(ctx, tx, p.UserID, p.HouseholdID, hash, now, now-int64(domain.SessionIdle.Seconds()))
		if readErr != nil {
			return Result{}, accountFailure(readErr)
		}
		var target []byte
		for _, item := range items {
			if domain.SessionHandle(item.TokenHash) == id {
				target = item.TokenHash
				break
			}
		}
		if len(target) == 0 {
			return Result{}, fault.New(fault.Missing, "session_not_found")
		}
		end = bytes.Equal(target, hash)
		err = s.store.DeleteOwnSession(ctx, tx, p.UserID, p.HouseholdID, target)
	}
	if err != nil {
		return Result{}, accountFailure(err)
	}
	if err = tx.Commit(); err != nil {
		return Result{}, accountFailure(err)
	}
	return Result{EndSession: end}, nil
}
