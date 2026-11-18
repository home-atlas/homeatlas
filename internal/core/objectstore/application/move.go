package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"

	backup "github.com/home-atlas/homeatlas/internal/core/backup/stream"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

// Collection is supplied by trusted host assembly, never by HTTP or a WASI guest.

func (s *Store) present(ctx context.Context, c Collection) (bool, error) {
	if !domain.SafeID.MatchString(c.ID) || !domain.TableName.MatchString(c.Table) || c.Directory == "" {
		return false, Invalid
	}
	return s.repository.Present(ctx, c.Table)
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
		counts, err := s.repository.MoveStats(ctx, house, c)
		if err != nil {
			return stats, err
		}
		stats.Remote += counts.Remote
		stats.RemoteBytes += counts.RemoteBytes
		stats.Local += counts.Local
		stats.LocalBytes += counts.LocalBytes

	}
	stats.PendingCleanup, err = s.repository.PendingCleanup(ctx, house)
	return stats, err
}

// MoveNext commits at most one verified original; the durable cleanup record allows retry after a crash.
func (s *Store) MoveNext(ctx context.Context, house, revision string, collections []Collection, authorize func(unitofwork.Transaction) error) (MoveResult, error) {
	result := MoveResult{}
	if err := authorize(nil); err != nil {
		return result, err
	}
	settings, Profile, err := s.current(ctx, house)
	if err != nil {
		return result, err
	}
	if revision != settings.Revision {
		return result, Conflict
	}
	old, err := s.repository.NextCleanup(ctx, house)
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
		item, err := s.repository.NextMove(ctx, house, c, Profile != "")
		if errors.Is(err, unitofwork.ErrNotFound) {
			continue
		}
		if err != nil {
			return result, err
		}
		if err = s.moveOne(ctx, house, revision, c, item, Profile, settings.S3, authorize); err != nil {
			return result, err
		}
		result.Moved = true
		result.MoveStats, err = s.MovePreview(ctx, house, collections)
		return result, err
	}
	result.MoveStats, err = s.MovePreview(ctx, house, collections)
	return result, err
}
func (s *Store) moveOne(ctx context.Context, house, revision string, c Collection, item MoveItem, targetProfile string, target S3Config, authorize func(unitofwork.Transaction) error) error {
	if !domain.SafeID.MatchString(item.Key) || item.Size < 1 || item.Size > 20<<20 || len(item.Digest) != 64 {
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
	old := Obsolete{ID: rand.Text(), Module: c.ID, Key: item.Key}
	var src *os.File
	var closeSource func()
	if targetProfile != "" {
		src, err = root.Open(item.Key)
		closeSource = func() {
			if src != nil {
				src.Close()
			}
		}
	} else {
		location, readErr := s.repository.Location(ctx, c.ID, item.Key)
		err = readErr
		old.Profile = location.Profile
		old.Object = location.Object
		if err != nil {
			return Unavailable
		}
		owner, _, profileErr := s.Profile(ctx, old.Profile)
		if profileErr != nil || owner != house {
			return Unavailable
		}
		src, closeSource, err = s.Open(ctx, c.ID, item.Key)
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
		pending = &Pending{store: s, Location: domain.Location{Profile: targetProfile, Module: c.ID, Key: next, Object: domain.ObjectKey(target, house, c.ID, next), Size: item.Size, Digest: item.Digest}, config: target}
		if err = s.remote.Put(ctx, target, pending.Object, src, item.Size); err != nil {
			return Unavailable
		}
		_, cleanup, err := s.remote.Fetch(ctx, target, pending.Object, item.Size, item.Digest)
		if err != nil {
			return Unavailable
		}
		cleanup()
	} else {
		dst, err := root.OpenFile(next, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return Unavailable
		}
		_, copyErr := io.Copy(backup.StreamWriter(dst), io.LimitReader(src, item.Size+1))
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
	_, fresh, err := s.repository.Settings(ctx, tx, house)
	if err != nil && !errors.Is(err, unitofwork.ErrNotFound) {
		return err
	}
	if fresh != revision {
		return Conflict
	}
	var location *domain.Location
	if pending != nil {
		location = &pending.Location
	}
	referenced, err := s.repository.PublishMove(ctx, tx, house, c, item, next, old, location)
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
func verifyOriginal(file *os.File, item MoveItem) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != item.Size {
		return Unavailable
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(backup.StreamReader(file), item.Size+1))
	if err != nil || n != item.Size || hex.EncodeToString(hash.Sum(nil)) != item.Digest {
		return Unavailable
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}
func (s *Store) clean(ctx context.Context, house string, old Obsolete, collections []Collection) error {
	if old.Profile != "" {
		owner, c, err := s.Profile(ctx, old.Profile)
		if err != nil || owner != house {
			return Unavailable
		}
		if err = s.remote.Delete(ctx, c, old.Object); err != nil {
			return Unavailable
		}
	} else {
		var directory string
		for _, c := range collections {
			if c.ID == old.Module {
				directory = c.Directory
				break
			}
		}
		if directory == "" || !domain.SafeID.MatchString(old.Key) {
			return Invalid
		}
		root, err := os.OpenRoot(directory)
		if err != nil && !os.IsNotExist(err) {
			return Unavailable
		}
		if err == nil {
			err = root.Remove(old.Key)
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
	return s.repository.FinishCleanup(ctx, house, old.ID)
}
