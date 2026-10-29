package plugins

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/backup"
)

// Backups pin approved code separately from data. Recovery never executes or
// installs code from an archive. Version 1 stateless packages remain readable.
type Backup struct{ Package *Package }

func (b Backup) ID() string { return b.Package.Manifest.ID }
func (b Backup) Version() int {
	if b.Package.Manifest.Storage == "shared" {
		return 3
	}
	if b.Package.Manifest.Storage == "user" {
		return 2
	}
	return 1
}
func (Backup) MaxFileBytes() int64 { return 1 }
func (b Backup) table() string     { return "plugin_" + strings.ReplaceAll(b.ID(), "-", "_") + "_package" }
func (b Backup) recordsTable() string {
	return "plugin_" + strings.ReplaceAll(b.ID(), "-", "_") + "_records"
}
func (b Backup) Tables() []string {
	tables := []string{"CREATE TABLE " + b.table() + " (sha256 TEXT NOT NULL)"}
	if b.Version() >= 2 {
		tables = append(tables, "CREATE TABLE "+b.recordsTable()+" (user_id TEXT NOT NULL, member_id TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY(member_id,key)) WITHOUT ROWID")
	}
	if b.Version() == 3 {
		tables = append(tables, b.sharedTables()...)
	}
	return tables
}
func (b Backup) Present(ctx context.Context, q backup.Queryer) (bool, error) {
	var present bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM module_states WHERE id=?)", b.ID()).Scan(&present)
	if err != nil || !present || b.Version() == 1 {
		return present, err
	}
	var tables int
	err = q.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN ('plugin_storage_scopes','plugin_storage_records')").Scan(&tables)
	if err != nil || tables != 2 || b.Version() != 3 {
		return tables == 2, err
	}
	err = q.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name IN ('plugin_shared_scopes','plugin_shared_records','plugin_shared_grants')").Scan(&tables)
	return tables == 3, err
}
func (b Backup) Empty(ctx context.Context, q backup.Queryer) (bool, error) {
	if b.Version() == 1 {
		return true, nil
	}
	var empty bool
	err := q.QueryRowContext(ctx, "SELECT NOT EXISTS(SELECT 1 FROM plugin_storage_records WHERE plugin_id=?) AND NOT EXISTS(SELECT 1 FROM plugin_shared_records WHERE plugin_id=?)", b.ID(), b.ID()).Scan(&empty)
	return empty, err
}
func (b Backup) Export(ctx context.Context, q backup.Queryer, tx *sql.Tx, household string, _ bool) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+b.table()+" VALUES(?)", b.Package.Digest); err != nil {
		return err
	}
	if b.Version() == 1 {
		return nil
	}
	// Reclaim consumed clean database/WAL pages as large plugin datasets stream.
	// This is advice only: SQLite retains control of syncing and durability.
	var source Store
	if err := q.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&source.path); err != nil {
		return err
	}
	source.discardCache()
	defer source.discardCache()
	rows, err := q.QueryContext(ctx, `SELECT s.user_id,r.member_id,r.key,r.value
 FROM plugin_storage_records r JOIN plugin_storage_scopes s
 ON s.plugin_id=r.plugin_id AND s.household_id=r.household_id AND s.member_id=r.member_id
 JOIN identity_member_ids m ON m.id=s.member_id AND m.user_id=s.user_id AND m.household_id=s.household_id
 WHERE r.plugin_id=? AND r.household_id=? ORDER BY r.member_id,r.key`, b.ID(), household)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var user, member, key, value string
		if err := rows.Scan(&user, &member, &key, &value); err != nil {
			return err
		}
		if !validStorageRecord(user, member, key, value) {
			return backup.Invalid
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+b.recordsTable()+" VALUES(?,?,?,?)", user, member, key, value); err != nil {
			return err
		}
		count++
		if count%512 == 0 {
			source.discardCache()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if b.Version() == 3 {
		return b.exportShared(ctx, q, tx, household, source.discardCache)
	}
	return nil
}
func validStorageRecord(user, member, key, value string) bool {
	return storageIdentity.MatchString(user) && storageIdentity.MatchString(member) && storageKey.MatchString(key) && len(value) <= StorageValueBytes && utf8.ValidString(value)
}

// Only one row is held at a time, irrespective of the size of a plugin's data.
func (b Backup) walk(ctx context.Context, q backup.Queryer, visit func(string, string, string, string) error) (int, error) {
	// Bound decoded columns before copying untrusted archive values into Go.
	rows, err := q.QueryContext(ctx, "SELECT substr(CAST(r.user_id AS BLOB),1,65),substr(CAST(r.member_id AS BLOB),1,65),substr(CAST(r.key AS BLOB),1,129),substr(CAST(r.value AS BLOB),1,16385),"+
		"typeof(r.user_id)='text' AND typeof(r.member_id)='text' AND typeof(r.key)='text' AND typeof(r.value)='text' AND EXISTS(SELECT 1 FROM members m WHERE m.id=r.user_id AND m.member_id=r.member_id) FROM "+b.recordsTable()+" r ORDER BY r.member_id,r.key")
	if err != nil {
		return 0, backup.Invalid
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var user, member, key, value string
		var active bool
		if err := rows.Scan(&user, &member, &key, &value, &active); err != nil || !active || !validStorageRecord(user, member, key, value) {
			return 0, backup.Invalid
		}
		if visit != nil {
			if err := visit(user, member, key, value); err != nil {
				return 0, err
			}
		}
		count++
	}
	return count, rows.Err()
}
func (b Backup) Inspect(ctx context.Context, q backup.Queryer, _ string) (backup.Contents, error) {
	rows, err := q.QueryContext(ctx, "SELECT substr(CAST(sha256 AS BLOB),1,65) FROM "+b.table())
	if err != nil {
		return backup.Contents{}, backup.Invalid
	}
	if !rows.Next() {
		rows.Close()
		return backup.Contents{}, backup.Invalid
	}
	var digest string
	if err := rows.Scan(&digest); err != nil || rows.Next() || rows.Err() != nil {
		rows.Close()
		return backup.Contents{}, backup.Invalid
	}
	rows.Close()
	if digest != b.Package.Digest {
		return backup.Contents{}, backup.ModuleRequired
	}
	if b.Version() == 1 {
		return backup.Contents{}, nil
	}
	count, err := b.walk(ctx, q, nil)
	if err != nil {
		return backup.Contents{}, err
	}
	if b.Version() == 3 {
		shared, err := b.inspectShared(ctx, q)
		return backup.Contents{Records: count + shared}, err
	}
	return backup.Contents{Records: count}, nil
}
func (Backup) OpenFile(string) (*os.File, error) { return nil, os.ErrNotExist }
func (b Backup) Prepare(ctx context.Context, q backup.Queryer, _ string, _ string) (backup.Plan, error) {
	if _, err := b.Inspect(ctx, q, ""); err != nil {
		return nil, err
	}
	return &storageRestore{provider: b, source: q}, nil
}

type storageRestore struct {
	provider Backup
	source   backup.Queryer
}

func (p *storageRestore) Apply(ctx context.Context, tx *sql.Tx, rc backup.RestoreContext) error {
	if p.provider.Version() == 1 {
		return nil
	}
	// Replace only this included plugin's household data. Current identities and
	// permissions win; new-installation recovery remaps validated memberships.
	rows, err := tx.QueryContext(ctx, "SELECT user_id,id FROM identity_member_ids WHERE household_id=?", rc.HouseholdID)
	if err != nil {
		return err
	}
	members := map[string]string{}
	for rows.Next() {
		var user, member string
		if err := rows.Scan(&user, &member); err != nil {
			rows.Close()
			return err
		}
		members[user] = member
		if len(members) > 1000 {
			rows.Close()
			return backup.Invalid
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM plugin_storage_records WHERE plugin_id=? AND household_id=?", p.provider.ID(), rc.HouseholdID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE plugin_storage_scopes SET revision=revision+1 WHERE plugin_id=? AND household_id=?", p.provider.ID(), rc.HouseholdID); err != nil {
		return err
	}
	_, err = p.provider.walk(ctx, p.source, func(user, sourceMember, key, value string) error {
		member, active := members[user]
		if !active {
			return nil
		}
		if rc.RecoveryMemberships != nil {
			if rc.RecoveryMemberships[user] != sourceMember {
				return backup.Invalid
			}
		} else if sourceMember != member {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_storage_scopes VALUES(?,?,?,?,1) ON CONFLICT(plugin_id,household_id,member_id) DO UPDATE SET user_id=excluded.user_id`, p.provider.ID(), rc.HouseholdID, member, user); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO plugin_storage_records VALUES(?,?,?,?,?)", p.provider.ID(), rc.HouseholdID, member, key, value)
		return err
	})
	if err != nil {
		return err
	}
	if p.provider.Version() == 3 {
		return p.applyShared(ctx, tx, rc, members)
	}
	return nil
}
func (*storageRestore) Finish(bool, bool) {}
