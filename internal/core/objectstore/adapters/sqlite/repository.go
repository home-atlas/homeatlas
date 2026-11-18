package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Repository struct{ db *sql.DB }

func (s *Repository) Begin(ctx context.Context) (unitofwork.Transaction, error) {
	return s.db.BeginTx(ctx, nil)
}
func (s *Repository) Profile(ctx context.Context, ID string) (string, []byte, error) {
	return readProfile(ctx, s.db, ID)
}
func readProfile(ctx context.Context, q backup.Queryer, ID string) (string, []byte, error) {
	var house string
	var sealed []byte
	err := q.QueryRowContext(ctx, "SELECT household_id,sealed FROM file_storage_profiles WHERE id=?", ID).Scan(&house, &sealed)
	return house, sealed, database.Normalize(err)
}

func (s *Repository) Settings(ctx context.Context, tx unitofwork.Transaction, home string) (string, string, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return "", "", err
	}
	var ID, revision string
	err = q.QueryRowContext(ctx, "SELECT profile_id,revision FROM file_storage_settings WHERE household_id=?", home).Scan(&ID, &revision)
	return ID, revision, database.Normalize(err)
}
func (s *Repository) Profiles(ctx context.Context, home string) (map[string][]byte, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,sealed FROM file_storage_profiles WHERE household_id=?", home)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var Profile string
		var raw []byte
		if err := rows.Scan(&Profile, &raw); err != nil {
			return nil, err
		}
		out[Profile] = raw
	}
	return out, rows.Err()
}
func (s *Repository) SaveSettings(ctx context.Context, tx unitofwork.Transaction, house, ID string, sealed []byte, refresh map[string][]byte, revision string) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	for Profile, updated := range refresh {
		if _, err := q.ExecContext(ctx, "UPDATE file_storage_profiles SET sealed=? WHERE id=? AND household_id=?", updated, Profile, house); err != nil {
			return err
		}
	}
	if ID != "" {
		if _, err := q.ExecContext(ctx, "INSERT INTO file_storage_profiles VALUES(?,?,?)", ID, house, sealed); err != nil {
			return err
		}
	}
	_, err = q.ExecContext(ctx, `INSERT INTO file_storage_settings VALUES(?,?,?) ON CONFLICT(household_id) DO UPDATE SET profile_id=excluded.profile_id,revision=excluded.revision`, house, ID, revision)
	return err
}
func (s *Repository) Publish(ctx context.Context, tx unitofwork.Transaction, p domain.Location) error {
	if tx == nil {
		return errors.New("publication requires a transaction")
	}
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO file_storage_locations VALUES(?,?,?,?,?,?)", p.Module, p.Key, p.Profile, p.Object, p.Size, p.Digest)
	return err
}

func readLocation(ctx context.Context, q backup.Queryer, Module, Key string) (Location, error) {
	var v Location
	err := q.QueryRowContext(ctx, "SELECT profile_id,object_key,size_bytes,sha256 FROM file_storage_locations WHERE module_id=? AND file_key=?", Module, Key).Scan(&v.Profile, &v.Object, &v.Size, &v.Digest)
	return v, database.Normalize(err)
}
func (s *Repository) Location(ctx context.Context, Module, Key string) (Location, error) {
	return readLocation(ctx, s.db, Module, Key)
}

// OpenFrom reads a consistent SQL backup view, rather than a newer live location.

func (s *Repository) Present(ctx context.Context, table string) (bool, error) {
	var Present bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)", table).Scan(&Present)
	return Present, err
}
func (s *Repository) MoveStats(ctx context.Context, house string, c Collection) (MoveStats, error) {
	var stats MoveStats
	rows, err := s.db.QueryContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_storage_locations l WHERE l.module_id=? AND l.file_key=d.file_key),count(*),COALESCE(sum(d.size_bytes),0) FROM `+c.Table+` d WHERE household_id=? AND file_key!='' GROUP BY 1`, c.ID, house)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var remote bool
		var count int
		var Size int64
		if err := rows.Scan(&remote, &count, &Size); err != nil {
			return stats, err
		}
		if remote {
			stats.Remote += count
			stats.RemoteBytes += Size
		} else {
			stats.Local += count
			stats.LocalBytes += Size
		}
	}
	return stats, rows.Err()
}
func (s *Repository) PendingCleanup(ctx context.Context, house string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM file_storage_cleanup WHERE household_id=?", house).Scan(&count)
	return count, err
}
func (s *Repository) NextCleanup(ctx context.Context, house string) (Obsolete, error) {
	var old Obsolete
	err := s.db.QueryRowContext(ctx, "SELECT id,module_id,file_key,profile_id,object_key FROM file_storage_cleanup WHERE household_id=? ORDER BY id LIMIT 1", house).Scan(&old.ID, &old.Module, &old.Key, &old.Profile, &old.Object)
	return old, database.Normalize(err)
}
func (s *Repository) NextMove(ctx context.Context, house string, c Collection, toRemote bool) (MoveItem, error) {
	var item MoveItem
	condition := "EXISTS"
	if toRemote {
		condition = "NOT EXISTS"
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,file_key,size_bytes,sha256 FROM `+c.Table+` d WHERE household_id=? AND file_key!='' AND `+condition+`(SELECT 1 FROM file_storage_locations l WHERE l.module_id=? AND l.file_key=d.file_key) ORDER BY id LIMIT 1`, house, c.ID).Scan(&item.ID, &item.Key, &item.Size, &item.Digest)
	return item, database.Normalize(err)
}
func (s *Repository) PublishMove(ctx context.Context, tx unitofwork.Transaction, house string, c Collection, item MoveItem, next string, old Obsolete, pending *domain.Location) (bool, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return false, err
	}
	updated, err := q.ExecContext(ctx, `UPDATE `+c.Table+` SET file_key=? WHERE household_id=? AND id=? AND file_key=?`, next, house, item.ID, item.Key)
	if err != nil {
		return false, err
	}
	n, err := updated.RowsAffected()
	if err != nil || n != 1 {
		return false, domain.Conflict
	}
	if pending != nil {
		if err := s.Publish(ctx, tx, *pending); err != nil {
			return false, err
		}
	}
	var referenced bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+c.Table+` WHERE file_key=?)`, item.Key).Scan(&referenced); err != nil {
		return false, err
	}
	if !referenced {
		if _, err := q.ExecContext(ctx, "DELETE FROM file_storage_locations WHERE module_id=? AND file_key=?", c.ID, item.Key); err != nil {
			return false, err
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO file_storage_cleanup VALUES(?,?,?,?,?,?)", old.ID, house, old.Module, old.Key, old.Profile, old.Object); err != nil {
			return false, err
		}
	}
	return referenced, nil
}
func (s *Repository) FinishCleanup(ctx context.Context, house, ID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM file_storage_cleanup WHERE id=? AND household_id=?", ID, house)
	return err
}

func New(db *sql.DB) *Repository { return &Repository{db: db} }
func Migrate(ctx context.Context, db *sql.DB) error {
	return database.Apply(ctx, db, "core.file-storage", []database.Migration{{Version: 1, SQL: `
 CREATE TABLE file_storage_profiles(id TEXT PRIMARY KEY, household_id TEXT NOT NULL, sealed BLOB NOT NULL);
 CREATE TABLE file_storage_settings(household_id TEXT PRIMARY KEY, profile_id TEXT NOT NULL, revision TEXT NOT NULL);
 CREATE TABLE file_storage_locations(module_id TEXT NOT NULL, file_key TEXT NOT NULL, profile_id TEXT NOT NULL REFERENCES file_storage_profiles(id), object_key TEXT NOT NULL, size_bytes INTEGER NOT NULL, sha256 TEXT NOT NULL, PRIMARY KEY(module_id,file_key));
 `}, {Version: 2, SQL: `
 CREATE TABLE file_storage_cleanup(id TEXT PRIMARY KEY,household_id TEXT NOT NULL,module_id TEXT NOT NULL,file_key TEXT NOT NULL,profile_id TEXT NOT NULL,object_key TEXT NOT NULL);
 CREATE INDEX file_storage_cleanup_household ON file_storage_cleanup(household_id);
 `}})
}

func SnapshotLocation(ctx context.Context, q backup.Queryer, Module, Key string) (domain.Location, string, []byte, error) {
	Location, err := readLocation(ctx, q, Module, Key)
	if err != nil {
		return Location, "", nil, err
	}
	home, sealed, err := readProfile(ctx, q, Location.Profile)
	return Location, home, sealed, err
}
