package plugins

import (
	"context"
	"database/sql"
	"errors"

	"github.com/home-atlas/homeatlas/internal/core/backup"
)

func (b Backup) sharedTable() string { return b.table() + "_shared" }
func (b Backup) grantsTable() string { return b.table() + "_grants" }
func (b Backup) sharedTables() []string {
	return []string{
		"CREATE TABLE " + b.sharedTable() + " (key TEXT PRIMARY KEY,record_id TEXT NOT NULL UNIQUE,owner_user_id TEXT NOT NULL,owner_member_id TEXT NOT NULL,value TEXT NOT NULL) WITHOUT ROWID",
		"CREATE TABLE " + b.grantsTable() + " (record_id TEXT NOT NULL,user_id TEXT NOT NULL,member_id TEXT NOT NULL,permission TEXT NOT NULL,PRIMARY KEY(record_id,user_id),FOREIGN KEY(record_id) REFERENCES " + b.sharedTable() + "(record_id)) WITHOUT ROWID",
	}
}
func validSharedRecord(r sharedRecord) bool {
	return storageKey.MatchString(r.Key) && storageIdentity.MatchString(r.ID) && storageIdentity.MatchString(r.Owner) && (r.Member == "" || storageIdentity.MatchString(r.Member)) && validStorageRecord(r.Owner, r.ID, r.Key, r.Value)
}
func (b Backup) exportShared(ctx context.Context, q backup.Queryer, out *sql.Tx, home string, discard func()) error {
	rows, err := q.QueryContext(ctx, "SELECT key,record_id,owner_user_id,owner_member_id,value FROM plugin_shared_records WHERE plugin_id=? AND household_id=? ORDER BY key", b.ID(), home)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		r := sharedRecord{}
		if err := rows.Scan(&r.Key, &r.ID, &r.Owner, &r.Member, &r.Value); err != nil {
			rows.Close()
			return err
		}
		if !validSharedRecord(r) {
			rows.Close()
			return backup.Invalid
		}
		if _, err := out.ExecContext(ctx, "INSERT INTO "+b.sharedTable()+" VALUES(?,?,?,?,?)", r.Key, r.ID, r.Owner, r.Member, r.Value); err != nil {
			rows.Close()
			return err
		}
		count++
		if count%512 == 0 {
			discard()
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = q.QueryContext(ctx, `SELECT g.record_id,g.user_id,g.member_id,g.permission FROM plugin_shared_grants g
 JOIN identity_member_ids m ON m.user_id=g.user_id AND m.household_id=g.household_id AND m.id=g.member_id
 WHERE g.plugin_id=? AND g.household_id=? AND EXISTS(SELECT 1 FROM identity_permissions p WHERE p.user_id=g.user_id AND p.household_id=g.household_id AND p.capability=?)`, b.ID(), home, b.ID()+".read")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		g := sharedGrant{}
		if err := rows.Scan(&g.Record, &g.User, &g.Member, &g.Permission); err != nil {
			return err
		}
		if _, err := out.ExecContext(ctx, "INSERT INTO "+b.grantsTable()+" VALUES(?,?,?,?)", g.Record, g.User, g.Member, g.Permission); err != nil {
			return err
		}
	}
	return rows.Err()
}
func (b Backup) walkShared(ctx context.Context, q backup.Queryer, visit func(sharedRecord) error) (int, error) {
	rows, err := q.QueryContext(ctx, "SELECT substr(CAST(key AS BLOB),1,129),substr(CAST(record_id AS BLOB),1,65),substr(CAST(owner_user_id AS BLOB),1,65),substr(CAST(owner_member_id AS BLOB),1,65),substr(CAST(value AS BLOB),1,16385),typeof(key)='text' AND typeof(record_id)='text' AND typeof(owner_user_id)='text' AND typeof(owner_member_id)='text' AND typeof(value)='text' FROM "+b.sharedTable()+" ORDER BY key")
	if err != nil {
		return 0, backup.Invalid
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		r := sharedRecord{}
		var types bool
		if err := rows.Scan(&r.Key, &r.ID, &r.Owner, &r.Member, &r.Value, &types); err != nil || !types || !validSharedRecord(r) {
			return 0, backup.Invalid
		}
		if visit != nil {
			if err := visit(r); err != nil {
				return 0, err
			}
		}
		count++
	}
	return count, rows.Err()
}
func (b Backup) walkGrants(ctx context.Context, q backup.Queryer, visit func(sharedGrant) error) error {
	rows, err := q.QueryContext(ctx, "SELECT substr(CAST(g.record_id AS BLOB),1,65),substr(CAST(g.user_id AS BLOB),1,65),substr(CAST(g.member_id AS BLOB),1,65),substr(CAST(g.permission AS BLOB),1,6),typeof(g.record_id)='text' AND typeof(g.user_id)='text' AND typeof(g.member_id)='text' AND typeof(g.permission)='text' AND EXISTS(SELECT 1 FROM members m WHERE m.id=g.user_id AND m.member_id=g.member_id) AND EXISTS(SELECT 1 FROM "+b.sharedTable()+" r WHERE r.record_id=g.record_id AND r.owner_user_id!=g.user_id) FROM "+b.grantsTable()+" g ORDER BY g.record_id,g.user_id")
	if err != nil {
		return backup.Invalid
	}
	defer rows.Close()
	for rows.Next() {
		g := sharedGrant{}
		var valid bool
		if err := rows.Scan(&g.Record, &g.User, &g.Member, &g.Permission, &valid); err != nil || !valid || !storageIdentity.MatchString(g.Record) || !storageIdentity.MatchString(g.User) || !storageIdentity.MatchString(g.Member) || (g.Permission != "read" && g.Permission != "write") {
			return backup.Invalid
		}
		if visit != nil {
			if err := visit(g); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}
func (b Backup) inspectShared(ctx context.Context, q backup.Queryer) (int, error) {
	n, err := b.walkShared(ctx, q, nil)
	if err != nil {
		return 0, err
	}
	return n, b.walkGrants(ctx, q, nil)
}

func (p *storageRestore) applyShared(ctx context.Context, tx *sql.Tx, rc backup.RestoreContext, members map[string]string) error {
	b := p.provider
	home := rc.HouseholdID
	// Disk-backed transactional staging preserves current ACLs for intersection.
	// It is dropped before commit; errors/crashes roll back the staging DDL too.
	staging := b.table() + "_current_acl"
	if _, err := tx.ExecContext(ctx, "CREATE TABLE "+staging+" (record_id TEXT,user_id TEXT,member_id TEXT,permission TEXT,PRIMARY KEY(record_id,user_id,member_id)) WITHOUT ROWID"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+staging+" SELECT record_id,user_id,member_id,permission FROM plugin_shared_grants WHERE plugin_id=? AND household_id=?", b.ID(), home); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM plugin_shared_records WHERE plugin_id=? AND household_id=?", b.ID(), home); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO plugin_shared_scopes VALUES(?,?,1) ON CONFLICT(plugin_id,household_id) DO UPDATE SET revision=revision+1", b.ID(), home); err != nil {
		return err
	}
	_, err := b.walkShared(ctx, p.source, func(r sharedRecord) error {
		current, active := members[r.Owner]
		if rc.RecoveryMemberships != nil {
			if !active || rc.RecoveryMemberships[r.Owner] != r.Member {
				r.Member = ""
			} else {
				r.Member = current
			}
		} else if !active || current != r.Member {
			r.Member = ""
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO plugin_shared_records VALUES(?,?,?,?,?,?,?)", b.ID(), home, r.Key, r.ID, r.Owner, r.Member, r.Value)
		return err
	})
	if err != nil {
		return err
	}
	err = b.walkGrants(ctx, p.source, func(g sharedGrant) error {
		member, active := members[g.User]
		if !active {
			return nil
		}
		if rc.RecoveryMemberships != nil {
			if rc.RecoveryMemberships[g.User] != g.Member {
				return backup.Invalid
			}
		} else {
			if member != g.Member {
				return nil
			}
			var current string
			err := tx.QueryRowContext(ctx, "SELECT permission FROM "+staging+" WHERE record_id=? AND user_id=? AND member_id=?", g.Record, g.User, member).Scan(&current)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if current == "read" {
				g.Permission = "read"
			}
		}
		var canRead, canWrite bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_permissions WHERE household_id=? AND user_id=? AND capability=?),EXISTS(SELECT 1 FROM identity_permissions WHERE household_id=? AND user_id=? AND capability=?)", home, g.User, b.ID()+".read", home, g.User, b.ID()+".write").Scan(&canRead, &canWrite); err != nil {
			return err
		}
		if !canRead {
			return nil
		}
		if !canWrite {
			g.Permission = "read"
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO plugin_shared_grants SELECT plugin_id,household_id,record_id,key,?,?,? FROM plugin_shared_records WHERE plugin_id=? AND household_id=? AND record_id=?", g.User, member, g.Permission, b.ID(), home, g.Record)
		return err
	})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DROP TABLE "+staging)
	return err
}
