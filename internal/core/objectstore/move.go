package objectstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"sync"

	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
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
	return s.repository.present(ctx, c.Table)
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
		counts, err := s.repository.moveStats(ctx, house, c)
		if err != nil {
			return stats, err
		}
		stats.Remote += counts.Remote
		stats.RemoteBytes += counts.RemoteBytes
		stats.Local += counts.Local
		stats.LocalBytes += counts.LocalBytes

	}
	stats.PendingCleanup, err = s.repository.pendingCleanup(ctx, house)
	return stats, err
}

type moveItem struct {
	id, key, digest string
	size            int64
}
type obsolete struct{ id, module, key, profile, object string }

// MoveNext commits at most one verified original; the durable cleanup record allows retry after a crash.
func (s *Store) MoveNext(ctx context.Context, house, revision string, collections []Collection, authorize func(unitofwork.Transaction) error) (MoveResult, error) {
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
	old, err := s.repository.nextCleanup(ctx, house)
	if err == nil {
		if err = s.clean(ctx, house, old, collections); err != nil {
			return result, err
		}
		result.MoveStats, err = s.MovePreview(ctx, house, collections)
		return result, err
	}
	if !errors.Is(err, unitofwork.ErrNotFound) {
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
		item, err := s.repository.nextMove(ctx, house, c, profile != "")
		if errors.Is(err, unitofwork.ErrNotFound) {
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
func (s *Store) moveOne(ctx context.Context, house, revision string, c Collection, item moveItem, targetProfile string, target S3Config, authorize func(unitofwork.Transaction) error) error {
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
		location, readErr := s.repository.location(ctx, c.ID, item.key)
		err = readErr
		old.profile = location.profile
		old.object = location.object
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
	tx, err := s.repository.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = authorize(tx); err != nil {
		return err
	}
	_, fresh, err := s.repository.settings(ctx, tx, house)
	if err != nil && !errors.Is(err, unitofwork.ErrNotFound) {
		return err
	}
	if fresh != revision {
		return Conflict
	}
	referenced, err := s.repository.publishMove(ctx, tx, house, c, item, next, old, pending)
	if err != nil {
		return err
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
	return s.repository.finishCleanup(ctx, house, old.id)
}
