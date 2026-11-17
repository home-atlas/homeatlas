package plugins

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type storageRepository struct{ db *sql.DB }

func (s *storageRepository) Begin(ctx context.Context) (unitofwork.Transaction, error) {
	return s.db.BeginTx(ctx, nil)
}
func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	err := database.Apply(ctx, db, "core.plugin-storage", []database.Migration{{Version: 1, SQL: `
 CREATE TABLE plugin_storage_scopes (
   plugin_id TEXT NOT NULL, household_id TEXT NOT NULL, member_id TEXT NOT NULL,
   user_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0),
   PRIMARY KEY(plugin_id,household_id,member_id)) WITHOUT ROWID;
 CREATE TABLE plugin_storage_records (
   plugin_id TEXT NOT NULL, household_id TEXT NOT NULL, member_id TEXT NOT NULL,
   key TEXT NOT NULL CHECK(length(CAST(key AS BLOB)) BETWEEN 1 AND 128),
   value TEXT NOT NULL CHECK(length(CAST(value AS BLOB))<=16384),
   PRIMARY KEY(plugin_id,household_id,member_id,key),
   FOREIGN KEY(plugin_id,household_id,member_id)
     REFERENCES plugin_storage_scopes(plugin_id,household_id,member_id) ON DELETE CASCADE
 ) WITHOUT ROWID;`}, {Version: 2, SQL: `
 CREATE TABLE plugin_shared_scopes(plugin_id TEXT NOT NULL,household_id TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision>0),PRIMARY KEY(plugin_id,household_id)) WITHOUT ROWID;
 CREATE TABLE plugin_shared_records(
   plugin_id TEXT NOT NULL,household_id TEXT NOT NULL,key TEXT NOT NULL,record_id TEXT NOT NULL,
   owner_user_id TEXT NOT NULL,owner_member_id TEXT NOT NULL,value TEXT NOT NULL CHECK(length(CAST(value AS BLOB))<=16384),
   PRIMARY KEY(plugin_id,household_id,key),UNIQUE(plugin_id,household_id,record_id),UNIQUE(plugin_id,household_id,key,record_id)) WITHOUT ROWID;
 CREATE INDEX plugin_shared_owner ON plugin_shared_records(plugin_id,household_id,owner_user_id,owner_member_id,key);
 CREATE TABLE plugin_shared_grants(
   plugin_id TEXT NOT NULL,household_id TEXT NOT NULL,record_id TEXT NOT NULL,key TEXT NOT NULL,
   user_id TEXT NOT NULL,member_id TEXT NOT NULL,permission TEXT NOT NULL CHECK(permission IN ('read','write')),
   PRIMARY KEY(plugin_id,household_id,record_id,user_id),
   FOREIGN KEY(plugin_id,household_id,key,record_id) REFERENCES plugin_shared_records(plugin_id,household_id,key,record_id) ON DELETE CASCADE) WITHOUT ROWID;
 CREATE INDEX plugin_shared_recipient ON plugin_shared_grants(plugin_id,household_id,user_id,member_id,key);`}})
	if err != nil {
		return nil, err
	}
	store := &Store{repository: &storageRepository{db: db}}
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			return nil, err
		}
		if name == "main" {
			store.path = path
		}
	}
	return store, rows.Err()
}

func (s *storageRepository) allowed(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal, write bool) (bool, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return false, err
	}
	var allowed bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM identity_memberships m
 JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id
 JOIN module_states s ON s.id=? AND s.enabled=1
 WHERE mi.id=? AND m.user_id=? AND m.household_id=?
 AND COALESCE((SELECT enabled FROM household_module_states WHERE household_id=m.household_id AND module_id=s.id),1)=1
 AND EXISTS(SELECT 1 FROM identity_permissions WHERE user_id=m.user_id AND household_id=m.household_id AND capability=?)
 AND (?=0 OR EXISTS(SELECT 1 FROM identity_permissions WHERE user_id=m.user_id AND household_id=m.household_id AND capability=?)))`, plugin, p.MembershipID, p.UserID, p.HouseholdID, plugin+".read", write, plugin+".write").Scan(&allowed)
	return allowed, err
}
func (s *storageRepository) revision(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal) (int64, string, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return 0, "", err
	}
	var revision int64
	var owner string
	err = q.QueryRowContext(ctx, `SELECT revision,user_id FROM plugin_storage_scopes WHERE plugin_id=? AND household_id=? AND member_id=?`, plugin, p.HouseholdID, p.MembershipID).Scan(&revision, &owner)
	return revision, owner, database.Normalize(err)
}
func (s *storageRepository) keys(ctx context.Context, plugin string, p access.Principal, after string, limit int) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM plugin_storage_records WHERE plugin_id=? AND household_id=? AND member_id=? AND key>? ORDER BY key LIMIT ?`, plugin, p.HouseholdID, p.MembershipID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}
func (s *storageRepository) value(ctx context.Context, plugin string, p access.Principal, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM plugin_storage_records WHERE plugin_id=? AND household_id=? AND member_id=? AND key=?`, plugin, p.HouseholdID, p.MembershipID, key).Scan(&value)
	return value, database.Normalize(err)
}
func (s *storageRepository) writePrivate(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal, pending map[string]*string) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO plugin_storage_scopes VALUES(?,?,?,?,1) ON CONFLICT(plugin_id,household_id,member_id) DO UPDATE SET revision=revision+1`, plugin, p.HouseholdID, p.MembershipID, p.UserID); err != nil {
		return err
	}
	for key, value := range pending {
		if value == nil {
			_, err = q.ExecContext(ctx, `DELETE FROM plugin_storage_records WHERE plugin_id=? AND household_id=? AND member_id=? AND key=?`, plugin, p.HouseholdID, p.MembershipID, key)
		} else {
			_, err = q.ExecContext(ctx, `INSERT INTO plugin_storage_records VALUES(?,?,?,?,?) ON CONFLICT(plugin_id,household_id,member_id,key) DO UPDATE SET value=excluded.value`, plugin, p.HouseholdID, p.MembershipID, key, *value)
		}
		if err != nil {
			return fmt.Errorf("commit plugin records: %w", err)
		}
	}
	return nil
}
func (s *storageRepository) sharedRevision(ctx context.Context, tx unitofwork.Transaction, plugin, home string) (int64, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return 0, err
	}
	var v int64
	err = q.QueryRowContext(ctx, "SELECT revision FROM plugin_shared_scopes WHERE plugin_id=? AND household_id=?", plugin, home).Scan(&v)
	return v, database.Normalize(err)
}
func (s *storageRepository) hasCapability(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal, cap string) (bool, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return false, err
	}
	var ok bool
	err = q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_permissions WHERE user_id=? AND household_id=? AND capability=?)", p.UserID, p.HouseholdID, plugin+"."+cap).Scan(&ok)
	return ok, err
}
func (s *storageRepository) sharedRecord(ctx context.Context, tx unitofwork.Transaction, plugin, home, key string) (*sharedRecord, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return nil, err
	}
	r := &sharedRecord{Key: key}
	err = q.QueryRowContext(ctx, "SELECT record_id,owner_user_id,owner_member_id,value FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND key=?", plugin, home, key).Scan(&r.ID, &r.Owner, &r.Member, &r.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}
func (s *storageRepository) grant(ctx context.Context, tx unitofwork.Transaction, plugin string, p access.Principal, id string) (string, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return "", err
	}
	var permission string
	err = q.QueryRowContext(ctx, "SELECT permission FROM plugin_shared_grants WHERE plugin_id=? AND household_id=? AND record_id=? AND user_id=? AND member_id=?", plugin, p.HouseholdID, id, p.UserID, p.MembershipID).Scan(&permission)
	return permission, database.Normalize(err)
}
func (s *storageRepository) recipient(ctx context.Context, tx unitofwork.Transaction, plugin, home, user, permission string) (string, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return "", err
	}
	var member string
	err = q.QueryRowContext(ctx, `SELECT mi.id FROM identity_member_ids mi WHERE mi.household_id=? AND mi.user_id=?
 AND EXISTS(SELECT 1 FROM identity_permissions WHERE household_id=mi.household_id AND user_id=mi.user_id AND capability=?)
 AND (?='read' OR EXISTS(SELECT 1 FROM identity_permissions WHERE household_id=mi.household_id AND user_id=mi.user_id AND capability=?))`, home, user, plugin+".read", permission, plugin+".write").Scan(&member)
	return member, database.Normalize(err)
}

func (s *storageRepository) sharedKeys(ctx context.Context, plugin string, p access.Principal, after string, take int) (map[string]bool, error) {
	keys := map[string]bool{}
	for _, query := range []string{
		"SELECT key FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND owner_user_id=? AND owner_member_id=? AND key>? ORDER BY key LIMIT ?",
		"SELECT key FROM plugin_shared_grants WHERE plugin_id=? AND household_id=? AND user_id=? AND member_id=? AND key>? ORDER BY key LIMIT ?",
	} {
		rows, err := s.db.QueryContext(ctx, query, plugin, p.HouseholdID, p.UserID, p.MembershipID, after, take)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, err
			}
			keys[key] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return keys, nil
}
func (s *storageRepository) grants(ctx context.Context, plugin, home, id, after string, limit int) (map[string]sharedGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.user_id,g.member_id,g.permission FROM plugin_shared_grants g JOIN identity_member_ids m ON m.user_id=g.user_id AND m.household_id=g.household_id AND m.id=g.member_id WHERE g.plugin_id=? AND g.household_id=? AND g.record_id=? AND g.user_id>? ORDER BY g.user_id LIMIT ?`, plugin, home, id, after, limit)
	if err != nil {
		return nil, err
	}
	found := map[string]sharedGrant{}
	for rows.Next() {
		g := sharedGrant{}
		if err := rows.Scan(&g.User, &g.Member, &g.Permission); err != nil {
			rows.Close()
			return nil, err
		}
		found[g.User] = g
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return found, nil
}
func (s *storageRepository) sharedByID(ctx context.Context, tx unitofwork.Transaction, plugin, home, id string) (*sharedRecord, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return nil, err
	}
	r := &sharedRecord{}
	err = q.QueryRowContext(ctx, "SELECT key,record_id,owner_user_id,owner_member_id FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND record_id=?", plugin, home, id).Scan(&r.Key, &r.ID, &r.Owner, &r.Member)
	return r, database.Normalize(err)
}
func (s *storageRepository) writeShared(ctx context.Context, scope unitofwork.Transaction, plugin string, p access.Principal, pending map[string]sharedChange, grants map[grantKey]*sharedGrant) error {
	tx, err := database.Within(s.db, scope)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO plugin_shared_scopes VALUES(?,?,1) ON CONFLICT(plugin_id,household_id) DO UPDATE SET revision=revision+1", plugin, p.HouseholdID); err != nil {
		return err
	}
	for key, c := range pending {
		if c.before != nil && (c.after == nil || c.before.ID != c.after.ID) {
			if _, err := tx.ExecContext(ctx, "DELETE FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND key=?", plugin, p.HouseholdID, key); err != nil {
				return err
			}
		}
		if c.after != nil {
			r := c.after
			if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_shared_records VALUES(?,?,?,?,?,?,?) ON CONFLICT(plugin_id,household_id,key) DO UPDATE SET value=excluded.value`, plugin, p.HouseholdID, r.Key, r.ID, r.Owner, r.Member, r.Value); err != nil {
				return err
			}
		}
	}
	for k, g := range grants {
		if g == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM plugin_shared_grants WHERE plugin_id=? AND household_id=? AND record_id=? AND user_id=?", plugin, p.HouseholdID, k.record, k.user)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO plugin_shared_grants VALUES(?,?,?,?,?,?,?) ON CONFLICT(plugin_id,household_id,record_id,user_id) DO UPDATE SET member_id=excluded.member_id,permission=excluded.permission`, plugin, p.HouseholdID, g.Record, g.Key, g.User, g.Member, g.Permission)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
