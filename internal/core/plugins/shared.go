package plugins

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"slices"
	"unicode/utf8"
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

func (a *storageAction) currentSharedRevision(ctx context.Context, q rowQueryer) (int64, error) {
	var v int64
	err := q.QueryRowContext(ctx, "SELECT revision FROM plugin_shared_scopes WHERE plugin_id=? AND household_id=?", a.plugin, a.principal.HouseholdID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}
func (a *storageAction) requireCap(ctx context.Context, q rowQueryer, cap string) error {
	var ok bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_permissions WHERE user_id=? AND household_id=? AND capability=?)", a.principal.UserID, a.principal.HouseholdID, a.plugin+"."+cap).Scan(&ok)
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
func (a *storageAction) loadShared(ctx context.Context, q rowQueryer, key string) (*sharedRecord, error) {
	r := &sharedRecord{Key: key}
	err := q.QueryRowContext(ctx, "SELECT record_id,owner_user_id,owner_member_id,value FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND key=?", a.plugin, a.principal.HouseholdID, key).Scan(&r.ID, &r.Owner, &r.Member, &r.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}
func (a *storageAction) currentShared(ctx context.Context, key string) (*sharedRecord, error) {
	if c, ok := a.sharedPending[key]; ok {
		return c.after, nil
	}
	return a.loadShared(ctx, a.store.db, key)
}
func (a *storageAction) sharedAccess(ctx context.Context, q rowQueryer, r *sharedRecord, write bool) error {
	if r == nil {
		return ErrStorageDenied
	}
	if a.own(r) {
		return nil
	}
	var permission string
	err := q.QueryRowContext(ctx, "SELECT permission FROM plugin_shared_grants WHERE plugin_id=? AND household_id=? AND record_id=? AND user_id=? AND member_id=?", a.plugin, a.principal.HouseholdID, r.ID, a.principal.UserID, a.principal.MembershipID).Scan(&permission)
	if errors.Is(err, sql.ErrNoRows) {
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
func (a *storageAction) target(ctx context.Context, q rowQueryer, user, permission string) (string, error) {
	var member string
	err := q.QueryRowContext(ctx, `SELECT mi.id FROM identity_member_ids mi WHERE mi.household_id=? AND mi.user_id=?
 AND EXISTS(SELECT 1 FROM identity_permissions WHERE household_id=mi.household_id AND user_id=mi.user_id AND capability=?)
 AND (?='read' OR EXISTS(SELECT 1 FROM identity_permissions WHERE household_id=mi.household_id AND user_id=mi.user_id AND capability=?))`, a.principal.HouseholdID, user, a.plugin+".read", permission, a.plugin+".write").Scan(&member)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrStorageDenied
	}
	return member, err
}
func (a *storageAction) sharedCall(ctx context.Context, req storageRequest) (storageResponse, error) {
	if !a.shared {
		return storageResponse{}, ErrStorageDenied
	}
	a.sharedTouched = true
	if err := a.authorize(ctx, a.store.db, false); err != nil {
		return storageResponse{}, err
	}
	rev, err := a.currentSharedRevision(ctx, a.store.db)
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
		if err := a.sharedAccess(ctx, a.store.db, r, false); errors.Is(err, ErrStorageDenied) {
			return storageResponse{OK: true}, nil
		} else if err != nil {
			return storageResponse{}, err
		}
		return storageResponse{OK: true, Found: true, Value: &r.Value}, nil
	case "shared.set", "shared.delete":
		if a.readOnly {
			return storageResponse{}, ErrStorageDenied
		}
		if err := a.requireCap(ctx, a.store.db, "write"); err != nil {
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
			if err := a.sharedAccess(ctx, a.store.db, r, true); err != nil {
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
		if err := a.requireCap(ctx, a.store.db, "share"); err != nil {
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
			member, err := a.target(ctx, a.store.db, req.User, req.Permission)
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
	for _, query := range []string{
		"SELECT key FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND owner_user_id=? AND owner_member_id=? AND key>? ORDER BY key LIMIT ?",
		"SELECT key FROM plugin_shared_grants WHERE plugin_id=? AND household_id=? AND user_id=? AND member_id=? AND key>? ORDER BY key LIMIT ?",
	} {
		rows, err := a.store.db.QueryContext(ctx, query, a.plugin, a.principal.HouseholdID, a.principal.UserID, a.principal.MembershipID, req.After, take)
		if err != nil {
			return storageResponse{}, err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return storageResponse{}, err
			}
			keys[key] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return storageResponse{}, err
		}
	}
	for key, c := range a.sharedPending {
		if key > req.After {
			if c.after == nil {
				delete(keys, key)
			} else {
				err := a.sharedAccess(ctx, a.store.db, c.after, false)
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
	if err := a.requireCap(ctx, a.store.db, "share"); err != nil {
		return storageResponse{}, err
	}
	if req.Value != nil || req.User != "" || req.Permission != "" || (req.After != "" && !storageIdentity.MatchString(req.After)) || req.Limit < 0 || req.Limit > StoragePageKeys {
		return storageResponse{}, errStorageInvalid
	}
	limit := req.Limit
	if limit == 0 {
		limit = StoragePageKeys
	}
	rows, err := a.store.db.QueryContext(ctx, `SELECT g.user_id,g.member_id,g.permission FROM plugin_shared_grants g JOIN identity_member_ids m ON m.user_id=g.user_id AND m.household_id=g.household_id AND m.id=g.member_id WHERE g.plugin_id=? AND g.household_id=? AND g.record_id=? AND g.user_id>? ORDER BY g.user_id LIMIT ?`, a.plugin, a.principal.HouseholdID, r.ID, req.After, limit+len(a.grantsPending)+1)
	if err != nil {
		return storageResponse{}, err
	}
	found := map[string]sharedGrant{}
	for rows.Next() {
		g := sharedGrant{}
		if err := rows.Scan(&g.User, &g.Member, &g.Permission); err != nil {
			rows.Close()
			return storageResponse{}, err
		}
		found[g.User] = g
	}
	err = rows.Err()
	rows.Close()
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
func (a *storageAction) commitShared(ctx context.Context, tx *sql.Tx) error {
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
				r = &sharedRecord{}
				err := tx.QueryRowContext(ctx, "SELECT key,record_id,owner_user_id,owner_member_id FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND record_id=?", a.plugin, a.principal.HouseholdID, k.record).Scan(&r.Key, &r.ID, &r.Owner, &r.Member)
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
	p := a.principal
	if _, err := tx.ExecContext(ctx, "INSERT INTO plugin_shared_scopes VALUES(?,?,1) ON CONFLICT(plugin_id,household_id) DO UPDATE SET revision=revision+1", a.plugin, p.HouseholdID); err != nil {
		return err
	}
	for key, c := range a.sharedPending {
		if c.before != nil && (c.after == nil || c.before.ID != c.after.ID) {
			if _, err := tx.ExecContext(ctx, "DELETE FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND key=?", a.plugin, p.HouseholdID, key); err != nil {
				return err
			}
		}
		if c.after != nil {
			r := c.after
			if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_shared_records VALUES(?,?,?,?,?,?,?) ON CONFLICT(plugin_id,household_id,key) DO UPDATE SET value=excluded.value`, a.plugin, p.HouseholdID, r.Key, r.ID, r.Owner, r.Member, r.Value); err != nil {
				return err
			}
		}
	}
	for k, g := range a.grantsPending {
		if g == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM plugin_shared_grants WHERE plugin_id=? AND household_id=? AND record_id=? AND user_id=?", a.plugin, p.HouseholdID, k.record, k.user)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO plugin_shared_grants VALUES(?,?,?,?,?,?,?) ON CONFLICT(plugin_id,household_id,record_id,user_id) DO UPDATE SET member_id=excluded.member_id,permission=excluded.permission`, a.plugin, p.HouseholdID, g.Record, g.Key, g.User, g.Member, g.Permission)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
