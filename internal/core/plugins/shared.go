package plugins

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type sharedRecord struct{ Key, ID, Owner, Member, Value string }
type sharedChange struct{ before, after *sharedRecord }
type grantKey struct{ record, user string }
type sharedGrant struct {
	User       string `json:"user"`
	Permission string `json:"permission"`
	Member     string `json:"-"`
	Record     string `json:"-"`
	Key        string `json:"-"`
}

func (a *storageAction) currentSharedRevision(ctx context.Context, tx unitofwork.Transaction) (int64, error) {
	v, err := a.store.repository.sharedRevision(ctx, tx, a.plugin, a.principal.HouseholdID)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return 0, nil
	}
	return v, err
}
func (a *storageAction) requireCap(ctx context.Context, tx unitofwork.Transaction, cap string) error {
	ok, err := a.store.repository.hasCapability(ctx, tx, a.plugin, a.principal, cap)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStorageDenied
	}
	return nil
}
func (a *storageAction) own(r *sharedRecord) bool {
	return r != nil && r.Owner == a.principal.UserID && r.Member != "" && r.Member == a.principal.MembershipID
}
func (a *storageAction) loadShared(ctx context.Context, tx unitofwork.Transaction, key string) (*sharedRecord, error) {
	return a.store.repository.sharedRecord(ctx, tx, a.plugin, a.principal.HouseholdID, key)

}
func (a *storageAction) currentShared(ctx context.Context, key string) (*sharedRecord, error) {
	if c, ok := a.sharedPending[key]; ok {
		return c.after, nil
	}
	return a.loadShared(ctx, nil, key)
}
func (a *storageAction) sharedAccess(ctx context.Context, tx unitofwork.Transaction, r *sharedRecord, write bool) error {
	if r == nil {
		return ErrStorageDenied
	}
	if a.own(r) {
		return nil
	}
	permission, err := a.store.repository.grant(ctx, tx, a.plugin, a.principal, r.ID)
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
func (a *storageAction) target(ctx context.Context, tx unitofwork.Transaction, user, permission string) (string, error) {
	member, err := a.store.repository.recipient(ctx, tx, a.plugin, a.principal.HouseholdID, user, permission)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return "", ErrStorageDenied
	}
	return member, err
}
func (a *storageAction) sharedCall(ctx context.Context, req storageRequest) (storageResponse, error) {
	if !a.shared {
		return storageResponse{}, ErrStorageDenied
	}
	a.sharedTouched = true
	if err := a.authorize(ctx, nil, false); err != nil {
		return storageResponse{}, err
	}
	rev, err := a.currentSharedRevision(ctx, nil)
	if err != nil {
		return storageResponse{}, err
	}
	if rev != a.sharedRevision {
		return storageResponse{}, ErrStorageConflict
	}
	if req.Op == "shared.list" {
		return a.listShared(ctx, req)
	}
	if !storageKey.MatchString(req.Key) {
		return storageResponse{}, errStorageInvalid
	}
	r, err := a.currentShared(ctx, req.Key)
	if err != nil {
		return storageResponse{}, err
	}
	if req.Op == "shared.grants" {
		return a.listGrants(ctx, r, req)
	}
	if req.After != "" || req.Limit != 0 {
		return storageResponse{}, errStorageInvalid
	}
	switch req.Op {
	case "shared.get":
		if req.Value != nil || req.User != "" || req.Permission != "" {
			return storageResponse{}, errStorageInvalid
		}
		if err := a.sharedAccess(ctx, nil, r, false); errors.Is(err, ErrStorageDenied) {
			return storageResponse{OK: true}, nil
		} else if err != nil {
			return storageResponse{}, err
		}
		return storageResponse{OK: true, Found: true, Value: &r.Value}, nil
	case "shared.set", "shared.delete":
		if a.readOnly {
			return storageResponse{}, ErrStorageDenied
		}
		if err := a.requireCap(ctx, nil, "write"); err != nil {
			return storageResponse{}, err
		}
		if req.User != "" || req.Permission != "" {
			return storageResponse{}, errStorageInvalid
		}
		if req.Op == "shared.set" && (req.Value == nil || len(*req.Value) > StorageValueBytes || !utf8.ValidString(*req.Value)) {
			return storageResponse{}, errStorageInvalid
		}
		if req.Op == "shared.delete" && req.Value != nil {
			return storageResponse{}, errStorageInvalid
		}
		if r != nil {
			if err := a.sharedAccess(ctx, nil, r, true); err != nil {
				return storageResponse{}, err
			}
		}
		if req.Op == "shared.delete" && !a.own(r) {
			return storageResponse{}, ErrStorageDenied
		}
		change, exists := a.sharedPending[req.Key]
		if !exists {
			change.before = r
		}
		if req.Op == "shared.delete" {
			change.after = nil
			for k := range a.grantsPending {
				if k.record == r.ID {
					delete(a.grantsPending, k)
				}
			}
		} else {
			if r == nil {
				r = &sharedRecord{Key: req.Key, ID: rand.Text(), Owner: a.principal.UserID, Member: a.principal.MembershipID}
			}
			next := *r
			next.Value = *req.Value
			change.after = &next
		}
		a.sharedPending[req.Key] = change
		return storageResponse{OK: true}, nil
	case "shared.grant", "shared.revoke":
		if a.readOnly || !a.own(r) {
			return storageResponse{}, ErrStorageDenied
		}
		if err := a.requireCap(ctx, nil, "share"); err != nil {
			return storageResponse{}, err
		}
		if req.Value != nil || !storageIdentity.MatchString(req.User) || req.User == r.Owner {
			return storageResponse{}, errStorageInvalid
		}
		k := grantKey{r.ID, req.User}
		if req.Op == "shared.revoke" {
			if req.Permission != "" {
				return storageResponse{}, errStorageInvalid
			}
			a.grantsPending[k] = nil
		} else {
			if req.Permission != "read" && req.Permission != "write" {
				return storageResponse{}, errStorageInvalid
			}
			member, err := a.target(ctx, nil, req.User, req.Permission)
			if err != nil {
				return storageResponse{}, err
			}
			a.grantsPending[k] = &sharedGrant{User: req.User, Member: member, Permission: req.Permission, Record: r.ID, Key: r.Key}
		}
		return storageResponse{OK: true}, nil
	default:
		return storageResponse{}, errStorageInvalid
	}
}
func (a *storageAction) listShared(ctx context.Context, req storageRequest) (storageResponse, error) {
	if req.Key != "" || req.Value != nil || req.User != "" || req.Permission != "" || (req.After != "" && !storageKey.MatchString(req.After)) || req.Limit < 0 || req.Limit > StoragePageKeys {
		return storageResponse{}, errStorageInvalid
	}
	limit := req.Limit
	if limit == 0 {
		limit = StoragePageKeys
	}
	take := limit + len(a.sharedPending) + 1
	keys := map[string]bool{}
	keys, err := a.store.repository.sharedKeys(ctx, a.plugin, a.principal, req.After, take)
	if err != nil {
		return storageResponse{}, err
	}
	for key, c := range a.sharedPending {
		if key > req.After {
			if c.after == nil {
				delete(keys, key)
			} else {
				err := a.sharedAccess(ctx, nil, c.after, false)
				if err == nil {
					keys[key] = true
				} else if !errors.Is(err, ErrStorageDenied) {
					return storageResponse{}, err
				}
			}
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	slices.Sort(ordered)
	result := storageResponse{OK: true, Keys: ordered}
	if len(ordered) > limit {
		result.Keys = ordered[:limit]
		result.Next = result.Keys[limit-1]
	}
	return result, nil
}
func (a *storageAction) listGrants(ctx context.Context, r *sharedRecord, req storageRequest) (storageResponse, error) {
	a.sharedNeedsShare = true
	if !a.own(r) {
		return storageResponse{}, ErrStorageDenied
	}
	if err := a.requireCap(ctx, nil, "share"); err != nil {
		return storageResponse{}, err
	}
	if req.Value != nil || req.User != "" || req.Permission != "" || (req.After != "" && !storageIdentity.MatchString(req.After)) || req.Limit < 0 || req.Limit > StoragePageKeys {
		return storageResponse{}, errStorageInvalid
	}
	limit := req.Limit
	if limit == 0 {
		limit = StoragePageKeys
	}
	found, err := a.store.repository.grants(ctx, a.plugin, a.principal.HouseholdID, r.ID, req.After, limit+len(a.grantsPending)+1)
	if err != nil {
		return storageResponse{}, err
	}
	for k, g := range a.grantsPending {
		if k.record == r.ID && k.user > req.After {
			if g == nil {
				delete(found, k.user)
			} else {
				found[k.user] = *g
			}
		}
	}
	users := make([]string, 0, len(found))
	for user := range found {
		users = append(users, user)
	}
	slices.Sort(users)
	result := storageResponse{OK: true}
	if len(users) > limit {
		users = users[:limit]
		result.Next = users[limit-1]
	}
	for _, user := range users {
		result.Grants = append(result.Grants, found[user])
	}
	return result, nil
}
func (a *storageAction) commitShared(ctx context.Context, tx unitofwork.Transaction) error {
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
		if c.before != nil {
			if err := a.sharedAccess(ctx, tx, c.before, true); err != nil {
				return err
			}
			if c.after == nil && !a.own(c.before) {
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
		var r *sharedRecord
		if g != nil {
			if change, ok := a.sharedPending[g.Key]; ok {
				r = change.after
			} else {
				var err error
				r, err = a.loadShared(ctx, tx, g.Key)
				if err != nil {
					return err
				}
			}
		} else {
			for _, c := range a.sharedPending {
				if c.after != nil && c.after.ID == k.record {
					r = c.after
					break
				}
			}
			if r == nil {
				r, err = a.store.repository.sharedByID(ctx, tx, a.plugin, a.principal.HouseholdID, k.record)
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
	return a.store.repository.writeShared(ctx, tx, a.plugin, a.principal, a.sharedPending, a.grantsPending)
}
