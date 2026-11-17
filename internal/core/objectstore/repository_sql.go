package objectstore

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type sqliteRepository struct{ db *sql.DB }

func New(ctx context.Context, db *sql.DB, dir string) (*Store, error) {
	if err := database.Apply(ctx, db, "core.file-storage", []database.Migration{{Version: 1, SQL: `
 CREATE TABLE file_storage_profiles(id TEXT PRIMARY KEY, household_id TEXT NOT NULL, sealed BLOB NOT NULL);
 CREATE TABLE file_storage_settings(household_id TEXT PRIMARY KEY, profile_id TEXT NOT NULL, revision TEXT NOT NULL);
 CREATE TABLE file_storage_locations(module_id TEXT NOT NULL, file_key TEXT NOT NULL, profile_id TEXT NOT NULL REFERENCES file_storage_profiles(id), object_key TEXT NOT NULL, size_bytes INTEGER NOT NULL, sha256 TEXT NOT NULL, PRIMARY KEY(module_id,file_key));
 `}, {Version: 2, SQL: `
 CREATE TABLE file_storage_cleanup(id TEXT PRIMARY KEY,household_id TEXT NOT NULL,module_id TEXT NOT NULL,file_key TEXT NOT NULL,profile_id TEXT NOT NULL,object_key TEXT NOT NULL);
 CREATE INDEX file_storage_cleanup_household ON file_storage_cleanup(household_id);
 `}}); err != nil {
		return nil, err
	}
	return &Store{repository: &sqliteRepository{db: db}, dir: dir, HTTPClient: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *sqliteRepository) Begin(ctx context.Context) (unitofwork.Transaction, error) {
	return s.db.BeginTx(ctx, nil)
}
func (s *sqliteRepository) profile(ctx context.Context, id string) (string, []byte, error) {
	return readProfile(ctx, s.db, id)
}
func readProfile(ctx context.Context, q backup.Queryer, id string) (string, []byte, error) {
	var house string
	var sealed []byte
	err := q.QueryRowContext(ctx, "SELECT household_id,sealed FROM file_storage_profiles WHERE id=?", id).Scan(&house, &sealed)
	return house, sealed, database.Normalize(err)
}
func (s *Store) profileFrom(ctx context.Context, q backup.Queryer, id string) (string, S3Config, error) {
	house, sealed, err := readProfile(ctx, q, id)
	if err != nil {
		return house, S3Config{}, err
	}
	return s.decodeProfile(house, id, sealed)
}
func (s *sqliteRepository) settings(ctx context.Context, tx unitofwork.Transaction, home string) (string, string, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return "", "", err
	}
	var id, revision string
	err = q.QueryRowContext(ctx, "SELECT profile_id,revision FROM file_storage_settings WHERE household_id=?", home).Scan(&id, &revision)
	return id, revision, database.Normalize(err)
}
func (s *sqliteRepository) profiles(ctx context.Context, home string) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,sealed FROM file_storage_profiles WHERE household_id=?", home)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var profile string
		var raw []byte
		if err := rows.Scan(&profile, &raw); err != nil {
			return nil, err
		}
		out[profile] = raw
	}
	return out, rows.Err()
}
func (s *sqliteRepository) saveSettings(ctx context.Context, tx unitofwork.Transaction, house, id string, sealed []byte, refresh map[string][]byte, revision string) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	for profile, updated := range refresh {
		if _, err := q.ExecContext(ctx, "UPDATE file_storage_profiles SET sealed=? WHERE id=? AND household_id=?", updated, profile, house); err != nil {
			return err
		}
	}
	if id != "" {
		if _, err := q.ExecContext(ctx, "INSERT INTO file_storage_profiles VALUES(?,?,?)", id, house, sealed); err != nil {
			return err
		}
	}
	_, err = q.ExecContext(ctx, `INSERT INTO file_storage_settings VALUES(?,?,?) ON CONFLICT(household_id) DO UPDATE SET profile_id=excluded.profile_id,revision=excluded.revision`, house, id, revision)
	return err
}
func (s *sqliteRepository) publish(ctx context.Context, tx unitofwork.Transaction, p *Pending) error {
	if tx == nil {
		return errors.New("publication requires a transaction")
	}
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO file_storage_locations VALUES(?,?,?,?,?,?)", p.module, p.key, p.profile, p.object, p.size, p.digest)
	return err
}

type storedLocation struct {
	profile, object, digest string
	size                    int64
}

func readLocation(ctx context.Context, q backup.Queryer, module, key string) (storedLocation, error) {
	var v storedLocation
	err := q.QueryRowContext(ctx, "SELECT profile_id,object_key,size_bytes,sha256 FROM file_storage_locations WHERE module_id=? AND file_key=?", module, key).Scan(&v.profile, &v.object, &v.size, &v.digest)
	return v, database.Normalize(err)
}
func (s *sqliteRepository) location(ctx context.Context, module, key string) (storedLocation, error) {
	return readLocation(ctx, s.db, module, key)
}

// OpenFrom reads a consistent SQL backup view, rather than a newer live location.
func (s *Store) OpenFrom(ctx context.Context, q backup.Queryer, module, key string) (*os.File, func(), error) {
	location, err := readLocation(ctx, q, module, key)
	if err != nil {
		return nil, func() {}, err
	}
	_, c, err := s.profileFrom(ctx, q, location.profile)
	if err != nil {
		return nil, func() {}, Unavailable
	}
	return s.openLocation(ctx, location, c)
}
func (s *sqliteRepository) present(ctx context.Context, table string) (bool, error) {
	var present bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)", table).Scan(&present)
	return present, err
}
func (s *sqliteRepository) moveStats(ctx context.Context, house string, c Collection) (MoveStats, error) {
	var stats MoveStats
	rows, err := s.db.QueryContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_storage_locations l WHERE l.module_id=? AND l.file_key=d.file_key),count(*),COALESCE(sum(d.size_bytes),0) FROM `+c.Table+` d WHERE household_id=? AND file_key!='' GROUP BY 1`, c.ID, house)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var remote bool
		var count int
		var size int64
		if err := rows.Scan(&remote, &count, &size); err != nil {
			return stats, err
		}
		if remote {
			stats.Remote += count
			stats.RemoteBytes += size
		} else {
			stats.Local += count
			stats.LocalBytes += size
		}
	}
	return stats, rows.Err()
}
func (s *sqliteRepository) pendingCleanup(ctx context.Context, house string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM file_storage_cleanup WHERE household_id=?", house).Scan(&count)
	return count, err
}
func (s *sqliteRepository) nextCleanup(ctx context.Context, house string) (obsolete, error) {
	var old obsolete
	err := s.db.QueryRowContext(ctx, "SELECT id,module_id,file_key,profile_id,object_key FROM file_storage_cleanup WHERE household_id=? ORDER BY id LIMIT 1", house).Scan(&old.id, &old.module, &old.key, &old.profile, &old.object)
	return old, database.Normalize(err)
}
func (s *sqliteRepository) nextMove(ctx context.Context, house string, c Collection, toRemote bool) (moveItem, error) {
	var item moveItem
	condition := "EXISTS"
	if toRemote {
		condition = "NOT EXISTS"
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,file_key,size_bytes,sha256 FROM `+c.Table+` d WHERE household_id=? AND file_key!='' AND `+condition+`(SELECT 1 FROM file_storage_locations l WHERE l.module_id=? AND l.file_key=d.file_key) ORDER BY id LIMIT 1`, house, c.ID).Scan(&item.id, &item.key, &item.size, &item.digest)
	return item, database.Normalize(err)
}
func (s *sqliteRepository) publishMove(ctx context.Context, tx unitofwork.Transaction, house string, c Collection, item moveItem, next string, old obsolete, pending *Pending) (bool, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return false, err
	}
	updated, err := q.ExecContext(ctx, `UPDATE `+c.Table+` SET file_key=? WHERE household_id=? AND id=? AND file_key=?`, next, house, item.id, item.key)
	if err != nil {
		return false, err
	}
	n, err := updated.RowsAffected()
	if err != nil || n != 1 {
		return false, Conflict
	}
	if pending != nil {
		if err := s.publish(ctx, tx, pending); err != nil {
			return false, err
		}
	}
	var referenced bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+c.Table+` WHERE file_key=?)`, item.key).Scan(&referenced); err != nil {
		return false, err
	}
	if !referenced {
		if _, err := q.ExecContext(ctx, "DELETE FROM file_storage_locations WHERE module_id=? AND file_key=?", c.ID, item.key); err != nil {
			return false, err
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO file_storage_cleanup VALUES(?,?,?,?,?,?)", old.id, house, old.module, old.key, old.profile, old.object); err != nil {
			return false, err
		}
	}
	return referenced, nil
}
func (s *sqliteRepository) finishCleanup(ctx context.Context, house, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM file_storage_cleanup WHERE id=? AND household_id=?", id, house)
	return err
}
