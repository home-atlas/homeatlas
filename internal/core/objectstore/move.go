package objectstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"sync"

	"github.com/home-atlas/homeatlas/internal/core/backup"
)

// Collection is supplied by trusted host assembly, never by HTTP or a WASI guest.
type Collection struct{ ID, Table, Directory string }
type MoveStats struct {
	Bucket         string `json:"bucket,omitempty"`
	Target         string `json:"target"`
	Revision       string `json:"revision"`
	Local          int    `json:"local"`
	Remote         int    `json:"remote"`
	LocalBytes     int64  `json:"localBytes"`
	RemoteBytes    int64  `json:"remoteBytes"`
	PendingCleanup int    `json:"pendingCleanup"`
}
type MoveResult struct {
	MoveStats
	Moved bool `json:"moved"`
}

var tableName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,100}$`)

func (s *Store) present(ctx context.Context, c Collection) (bool, error) {
	if !safeID.MatchString(c.ID) || !tableName.MatchString(c.Table) || c.Directory == "" {
		return false, Invalid
	}
	var present bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)", c.Table).Scan(&present)
	return present, err
}
func (s *Store) MovePreview(ctx context.Context, house string, collections []Collection) (MoveStats, error) {
	current, _, err := s.current(ctx, house)
	stats := MoveStats{Target: current.Provider, Revision: current.Revision, Bucket: current.S3.Bucket}
	if err != nil {
		return stats, err
	}
	for _, c := range collections {
		present, err := s.present(ctx, c)
		if err != nil {
			return stats, err
		}
		if !present {
			continue
		}
		rows, err := s.db.QueryContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_storage_locations l WHERE l.module_id=? AND l.file_key=d.file_key),count(*),COALESCE(sum(d.size_bytes),0) FROM `+c.Table+` d WHERE household_id=? AND file_key!='' GROUP BY 1`, c.ID, house)
		if err != nil {
			return stats, err
		}
		for rows.Next() {
			var remote bool
			var count int
			var size int64
			if err = rows.Scan(&remote, &count, &size); err != nil {
				break
			}
			if remote {
				stats.Remote += count
				stats.RemoteBytes += size
			} else {
				stats.Local += count
				stats.LocalBytes += size
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return stats, err
		}
	}
	err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM file_storage_cleanup WHERE household_id=?", house).Scan(&stats.PendingCleanup)
	return stats, err
}

type moveItem struct {
	id, key, digest string
	size            int64
}
type obsolete struct{ id, module, key, profile, object string }

// MoveNext commits at most one verified original; the durable cleanup record allows retry after a crash.
func (s *Store) MoveNext(ctx context.Context, house, revision string, collections []Collection, authorize func(*sql.Tx) error) (MoveResult, error) {
	result := MoveResult{}
	if err := authorize(nil); err != nil {
		return result, err
	}
	settings, profile, err := s.current(ctx, house)
	if err != nil {
		return result, err
	}
	if revision != settings.Revision {
		return result, Conflict
	}
	var old obsolete
	err = s.db.QueryRowContext(ctx, "SELECT id,module_id,file_key,profile_id,object_key FROM file_storage_cleanup WHERE household_id=? ORDER BY id LIMIT 1", house).Scan(&old.id, &old.module, &old.key, &old.profile, &old.object)
	if err == nil {
		if err = s.clean(ctx, house, old, collections); err != nil {
			return result, err
		}
		result.MoveStats, err = s.MovePreview(ctx, house, collections)
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	for _, c := range collections {
		present, err := s.present(ctx, c)
		if err != nil {
			return result, err
		}
		if !present {
			continue
		}
		var item moveItem
		condition := "EXISTS"
		if profile != "" {
			condition = "NOT EXISTS"
		}
		err = s.db.QueryRowContext(ctx, `SELECT id,file_key,size_bytes,sha256 FROM `+c.Table+` d WHERE household_id=? AND file_key!='' AND `+condition+`(SELECT 1 FROM file_storage_locations l WHERE l.module_id=? AND l.file_key=d.file_key) ORDER BY id LIMIT 1`, house, c.ID).Scan(&item.id, &item.key, &item.size, &item.digest)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return result, err
		}
		if err = s.moveOne(ctx, house, revision, c, item, profile, settings.S3, authorize); err != nil {
			return result, err
		}
		result.Moved = true
		result.MoveStats, err = s.MovePreview(ctx, house, collections)
		return result, err
	}
	result.MoveStats, err = s.MovePreview(ctx, house, collections)
	return result, err
}
func (s *Store) moveOne(ctx context.Context, house, revision string, c Collection, item moveItem, targetProfile string, target S3Config, authorize func(*sql.Tx) error) error {
	if !safeID.MatchString(item.key) || item.size < 1 || item.size > 20<<20 || len(item.digest) != 64 {
		return Invalid
	}
	if targetProfile == "" {
		if err := os.MkdirAll(c.Directory, 0700); err != nil {
			return Unavailable
		}
	}
	root, err := os.OpenRoot(c.Directory)
	if err != nil {
		return Unavailable
	}
	defer root.Close()
	old := obsolete{id: rand.Text(), module: c.ID, key: item.key}
	var src *os.File
	var closeSource func()
	if targetProfile != "" {
		src, err = root.Open(item.key)
		closeSource = func() {
			if src != nil {
				src.Close()
			}
		}
	} else {
		err = s.db.QueryRowContext(ctx, "SELECT profile_id,object_key FROM file_storage_locations WHERE module_id=? AND file_key=?", c.ID, item.key).Scan(&old.profile, &old.object)
		if err != nil {
			return Unavailable
		}
		owner, _, profileErr := s.profile(ctx, old.profile)
		if profileErr != nil || owner != house {
			return Unavailable
		}
		src, closeSource, err = s.Open(ctx, c.ID, item.key)
	}
	if err != nil {
		return Unavailable
	}
	closeSource = sync.OnceFunc(closeSource)
	defer closeSource()
	if err = verifyOriginal(src, item); err != nil {
		return err
	}
	next := rand.Text()
	committed := false
	var pending *Pending
	defer func() {
		if !committed {
			if pending != nil {
				pending.Abort()
			} else {
				root.Remove(next)
			}
		}
	}()
	if targetProfile != "" {
		pending = &Pending{store: s, profile: targetProfile, module: c.ID, key: next, object: objectKey(target, house, c.ID, next), size: item.size, digest: item.digest, config: target}
		if err = s.put(ctx, target, pending.object, src, item.size); err != nil {
			return Unavailable
		}
		_, cleanup, err := s.fetch(ctx, target, pending.object, item.size, item.digest)
		if err != nil {
			return Unavailable
		}
		cleanup()
	} else {
		dst, err := root.OpenFile(next, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return Unavailable
		}
		_, copyErr := io.Copy(backup.StreamWriter(dst), io.LimitReader(src, item.size+1))
		syncErr := dst.Sync()
		verifyErr := verifyOriginal(dst, item)
		backup.DiscardCache(dst)
		closeErr := dst.Close()
		if copyErr != nil || syncErr != nil || verifyErr != nil || closeErr != nil {
			return Unavailable
		}
		directory, err := root.Open(".")
		if err != nil {
			return Unavailable
		}
		err = directory.Sync()
		directory.Close()
		if err != nil {
			return Unavailable
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = authorize(tx); err != nil {
		return err
	}
	var fresh string
	err = tx.QueryRowContext(ctx, "SELECT revision FROM file_storage_settings WHERE household_id=?", house).Scan(&fresh)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if fresh != revision {
		return Conflict
	}
	updated, err := tx.ExecContext(ctx, `UPDATE `+c.Table+` SET file_key=? WHERE household_id=? AND id=? AND file_key=?`, next, house, item.id, item.key)
	if err != nil {
		return err
	}
	n, err := updated.RowsAffected()
	if err != nil || n != 1 {
		return Conflict
	}
	if pending != nil {
		if err = pending.Publish(ctx, tx); err != nil {
			return err
		}
	}
	var referenced bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+c.Table+` WHERE file_key=?)`, item.key).Scan(&referenced); err != nil {
		return err
	}
	if !referenced {
		if _, err = tx.ExecContext(ctx, "DELETE FROM file_storage_locations WHERE module_id=? AND file_key=?", c.ID, item.key); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO file_storage_cleanup VALUES(?,?,?,?,?,?)", old.id, house, old.module, old.key, old.profile, old.object); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	closeSource() // Required before removing a local source on platforms that lock open files.
	if !referenced {
		_ = s.clean(ctx, house, old, []Collection{c})
	} // The durable queue reports and retries failed cleanup.
	return nil
}
func verifyOriginal(file *os.File, item moveItem) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != item.size {
		return Unavailable
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(backup.StreamReader(file), item.size+1))
	if err != nil || n != item.size || hex.EncodeToString(hash.Sum(nil)) != item.digest {
		return Unavailable
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}
func (s *Store) clean(ctx context.Context, house string, old obsolete, collections []Collection) error {
	if old.profile != "" {
		owner, c, err := s.profile(ctx, old.profile)
		if err != nil || owner != house {
			return Unavailable
		}
		if err = s.deleteObject(ctx, c, old.object); err != nil {
			return Unavailable
		}
	} else {
		var directory string
		for _, c := range collections {
			if c.ID == old.module {
				directory = c.Directory
				break
			}
		}
		if directory == "" || !safeID.MatchString(old.key) {
			return Invalid
		}
		root, err := os.OpenRoot(directory)
		if err != nil && !os.IsNotExist(err) {
			return Unavailable
		}
		if err == nil {
			err = root.Remove(old.key)
			if err != nil && !os.IsNotExist(err) {
				root.Close()
				return Unavailable
			}
			directory, openErr := root.Open(".")
			if openErr != nil {
				root.Close()
				return Unavailable
			}
			syncErr := directory.Sync()
			directory.Close()
			root.Close()
			if syncErr != nil {
				return Unavailable
			}
		}
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM file_storage_cleanup WHERE id=? AND household_id=?", old.id, house)
	return err
}
