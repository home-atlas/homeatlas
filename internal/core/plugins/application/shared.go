package application

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

func (a *Action) currentSharedRevision(ctx context.Context, tx unitofwork.Transaction) (int64, error) {
	v, err := a.store.repository.SharedRevision(ctx, tx, a.plugin, a.principal.HouseholdID)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return 0, nil
	}
	return v, err
}
func (a *Action) requireCap(ctx context.Context, tx unitofwork.Transaction, cap string) error {
	ok, err := a.store.repository.HasCapability(ctx, tx, a.plugin, a.principal, cap)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStorageDenied
	}
	return nil
}
func (a *Action) own(r *SharedRecord) bool {
	return r != nil && r.Owner == a.principal.UserID && r.Member != "" && r.Member == a.principal.MembershipID
}
func (a *Action) loadShared(ctx context.Context, tx unitofwork.Transaction, key string) (*SharedRecord, error) {
	return a.store.repository.SharedRecord(ctx, tx, a.plugin, a.principal.HouseholdID, key)

}
func (a *Action) currentShared(ctx context.Context, key string) (*SharedRecord, error) {
	if c, ok := a.sharedPending[key]; ok {
		return c.After, nil
	}
	return a.loadShared(ctx, nil, key)
}
func (a *Action) sharedAccess(ctx context.Context, tx unitofwork.Transaction, r *SharedRecord, write bool) error {
	if r == nil {
		return ErrStorageDenied
	}
	if a.own(r) {
		return nil
	}
	permission, err := a.store.repository.Grant(ctx, tx, a.plugin, a.principal, r.ID)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return ErrStorageDenied
	}
	if err != nil {
		return err
	}
	if write && permission != "write" {
		return ErrStorageDenied
	}
	return nil
}
func (a *Action) target(ctx context.Context, tx unitofwork.Transaction, User, permission string) (string, error) {
	member, err := a.store.repository.Recipient(ctx, tx, a.plugin, a.principal.HouseholdID, User, permission)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return "", ErrStorageDenied
	}
	return member, err
}
func (a *Action) sharedCall(ctx context.Context, req StorageRequest) (StorageResponse, error) {
	if !a.shared {
		return StorageResponse{}, ErrStorageDenied
	}
	a.sharedTouched = true
	if err := a.authorize(ctx, nil, false); err != nil {
		return StorageResponse{}, err
	}
	rev, err := a.currentSharedRevision(ctx, nil)
	if err != nil {
		return StorageResponse{}, err
	}
	if rev != a.sharedRevision {
		return StorageResponse{}, ErrStorageConflict
	}
	if req.Op == "shared.list" {
		return a.listShared(ctx, req)
	}
	if !storageKey.MatchString(req.Key) {
		return StorageResponse{}, errStorageInvalid
	}
	r, err := a.currentShared(ctx, req.Key)
	if err != nil {
		return StorageResponse{}, err
	}
	if req.Op == "shared.grants" {
		return a.listGrants(ctx, r, req)
	}
	if req.After != "" || req.Limit != 0 {
		return StorageResponse{}, errStorageInvalid
	}
	switch req.Op {
	case "shared.get":
		if req.Value != nil || req.User != "" || req.Permission != "" {
			return StorageResponse{}, errStorageInvalid
		}
		if err := a.sharedAccess(ctx, nil, r, false); errors.Is(err, ErrStorageDenied) {
			return StorageResponse{OK: true}, nil
		} else if err != nil {
			return StorageResponse{}, err
		}
		return StorageResponse{OK: true, Found: true, Value: &r.Value}, nil
	case "shared.set", "shared.delete":
		if a.readOnly {
			return StorageResponse{}, ErrStorageDenied
		}
		if err := a.requireCap(ctx, nil, "write"); err != nil {
			return StorageResponse{}, err
		}
		if req.User != "" || req.Permission != "" {
			return StorageResponse{}, errStorageInvalid
		}
		if req.Op == "shared.set" && (req.Value == nil || len(*req.Value) > StorageValueBytes || !utf8.ValidString(*req.Value)) {
			return StorageResponse{}, errStorageInvalid
		}
		if req.Op == "shared.delete" && req.Value != nil {
			return StorageResponse{}, errStorageInvalid
		}
		if r != nil {
			if err := a.sharedAccess(ctx, nil, r, true); err != nil {
				return StorageResponse{}, err
			}
		}
		if req.Op == "shared.delete" && !a.own(r) {
			return StorageResponse{}, ErrStorageDenied
		}
		change, exists := a.sharedPending[req.Key]
		if !exists {
			change.Before = r
		}
		if req.Op == "shared.delete" {
			change.After = nil
			for k := range a.grantsPending {
				if k.Record == r.ID {
					delete(a.grantsPending, k)
				}
			}
		} else {
			if r == nil {
				r = &SharedRecord{Key: req.Key, ID: rand.Text(), Owner: a.principal.UserID, Member: a.principal.MembershipID}
			}
			next := *r
			next.Value = *req.Value
			change.After = &next
		}
		a.sharedPending[req.Key] = change
		return StorageResponse{OK: true}, nil
	case "shared.grant", "shared.revoke":
		if a.readOnly || !a.own(r) {
			return StorageResponse{}, ErrStorageDenied
		}
		if err := a.requireCap(ctx, nil, "share"); err != nil {
			return StorageResponse{}, err
		}
		if req.Value != nil || !storageIdentity.MatchString(req.User) || req.User == r.Owner {
			return StorageResponse{}, errStorageInvalid
		}
		k := GrantKey{Record: r.ID, User: req.User}
		if req.Op == "shared.revoke" {
			if req.Permission != "" {
				return StorageResponse{}, errStorageInvalid
			}
			a.grantsPending[k] = nil
		} else {
			if req.Permission != "read" && req.Permission != "write" {
				return StorageResponse{}, errStorageInvalid
			}
			member, err := a.target(ctx, nil, req.User, req.Permission)
			if err != nil {
				return StorageResponse{}, err
			}
			a.grantsPending[k] = &SharedGrant{User: req.User, Member: member, Permission: req.Permission, Record: r.ID, Key: r.Key}
		}
		return StorageResponse{OK: true}, nil
	default:
		return StorageResponse{}, errStorageInvalid
	}
}
func (a *Action) listShared(ctx context.Context, req StorageRequest) (StorageResponse, error) {
	if req.Key != "" || req.Value != nil || req.User != "" || req.Permission != "" || (req.After != "" && !storageKey.MatchString(req.After)) || req.Limit < 0 || req.Limit > StoragePageKeys {
		return StorageResponse{}, errStorageInvalid
	}
	limit := req.Limit
	if limit == 0 {
		limit = StoragePageKeys
	}
	take := limit + len(a.sharedPending) + 1
	keys := map[string]bool{}
	keys, err := a.store.repository.SharedKeys(ctx, a.plugin, a.principal, req.After, take)
	if err != nil {
		return StorageResponse{}, err
	}
	for key, c := range a.sharedPending {
		if key > req.After {
			if c.After == nil {
				delete(keys, key)
			} else {
				err := a.sharedAccess(ctx, nil, c.After, false)
				if err == nil {
					keys[key] = true
				} else if !errors.Is(err, ErrStorageDenied) {
					return StorageResponse{}, err
				}
			}
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	slices.Sort(ordered)
	result := StorageResponse{OK: true, Keys: ordered}
	if len(ordered) > limit {
		result.Keys = ordered[:limit]
		result.Next = result.Keys[limit-1]
	}
	return result, nil
}
func (a *Action) listGrants(ctx context.Context, r *SharedRecord, req StorageRequest) (StorageResponse, error) {
	a.sharedNeedsShare = true
	if !a.own(r) {
		return StorageResponse{}, ErrStorageDenied
	}
	if err := a.requireCap(ctx, nil, "share"); err != nil {
		return StorageResponse{}, err
	}
	if req.Value != nil || req.User != "" || req.Permission != "" || (req.After != "" && !storageIdentity.MatchString(req.After)) || req.Limit < 0 || req.Limit > StoragePageKeys {
		return StorageResponse{}, errStorageInvalid
	}
	limit := req.Limit
	if limit == 0 {
		limit = StoragePageKeys
	}
	found, err := a.store.repository.Grants(ctx, a.plugin, a.principal.HouseholdID, r.ID, req.After, limit+len(a.grantsPending)+1)
	if err != nil {
		return StorageResponse{}, err
	}
	for k, g := range a.grantsPending {
		if k.Record == r.ID && k.User > req.After {
			if g == nil {
				delete(found, k.User)
			} else {
				found[k.User] = *g
			}
		}
	}
	users := make([]string, 0, len(found))
	for User := range found {
		users = append(users, User)
	}
	slices.Sort(users)
	result := StorageResponse{OK: true}
	if len(users) > limit {
		users = users[:limit]
		result.Next = users[limit-1]
	}
	for _, User := range users {
		result.Grants = append(result.Grants, found[User])
	}
	return result, nil
}
func (a *Action) commitShared(ctx context.Context, tx unitofwork.Transaction) error {
	if !a.sharedTouched {
		return nil
	}
	if a.sharedNeedsShare {
		if err := a.requireCap(ctx, tx, "share"); err != nil {
			return err
		}
	}
	if a.readOnly && (len(a.sharedPending) > 0 || len(a.grantsPending) > 0) {
		return ErrStorageDenied
	}
	rev, err := a.currentSharedRevision(ctx, tx)
	if err != nil {
		return err
	}
	if rev != a.sharedRevision {
		return ErrStorageConflict
	}
	for _, c := range a.sharedPending {
		if err := a.requireCap(ctx, tx, "write"); err != nil {
			return err
		}
		if c.Before != nil {
			if err := a.sharedAccess(ctx, tx, c.Before, true); err != nil {
				return err
			}
			if c.After == nil && !a.own(c.Before) {
				return ErrStorageDenied
			}
		}
	}
	for k, g := range a.grantsPending {
		if a.readOnly {
			return ErrStorageDenied
		}
		if err := a.requireCap(ctx, tx, "share"); err != nil {
			return err
		}
		var r *SharedRecord
		if g != nil {
			if change, ok := a.sharedPending[g.Key]; ok {
				r = change.After
			} else {
				var err error
				r, err = a.loadShared(ctx, tx, g.Key)
				if err != nil {
					return err
				}
			}
		} else {
			for _, c := range a.sharedPending {
				if c.After != nil && c.After.ID == k.Record {
					r = c.After
					break
				}
			}
			if r == nil {
				r, err = a.store.repository.SharedByID(ctx, tx, a.plugin, a.principal.HouseholdID, k.Record)
				if err != nil {
					return err
				}

			}
		}
		if !a.own(r) {
			return ErrStorageDenied
		}
		if g != nil {
			member, err := a.target(ctx, tx, g.User, g.Permission)
			if err != nil {
				return err
			}
			if member != g.Member {
				return ErrStorageDenied
			}
		}
	}
	if len(a.sharedPending) == 0 && len(a.grantsPending) == 0 {
		return nil
	}
	return a.store.repository.WriteShared(ctx, tx, a.plugin, a.principal, a.sharedPending, a.grantsPending)
}
