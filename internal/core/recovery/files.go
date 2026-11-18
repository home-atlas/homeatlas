package recovery

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var originalKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func syncRoot(root *os.Root, name string) error {
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Temporary files never contain committed originals. Call only under the data lock,
// before any HTTP handler, guest or scheduled worker can create new ones.
func Temporary(ctx context.Context, directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".household-backup-") && entry.Name() != ".household-backup-" {
			if err := root.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
	}
	// Reject symlink directories rather than traversing somebody else's files.
	info, err := root.Lstat("file-staging")
	if errors.Is(err, os.ErrNotExist) {
		return syncRoot(root, ".")
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("file-staging is not a private directory")
	}
	staging, err := root.OpenRoot("file-staging")
	if err != nil {
		return err
	}
	defer staging.Close()
	f, err = staging.Open(".")
	if err != nil {
		return err
	}
	entries, err = f.ReadDir(-1)
	f.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), "original-") && !entry.IsDir() {
			if err := staging.Remove(entry.Name()); err != nil {
				return err
			}
		}
	}
	return errors.Join(syncRoot(staging, "."), syncRoot(root, "."))
}

// Originals quarantines unreferenced files instead of deleting potentially useful
// historical/damaged originals. The key is retained under a private recovery batch.
func Originals(ctx context.Context, directory string, referenced map[string]bool) (int, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("originals path is not a private directory")
	}
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	batch := ""
	count := 0
	for {
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return count, err
			}
			key := entry.Name()
			if referenced[key] || !originalKey.MatchString(key) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return count, err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if batch == "" {
				if err := root.Mkdir(".recovered", 0700); err != nil && !errors.Is(err, fs.ErrExist) {
					return count, err
				}
				info, err := root.Lstat(".recovered")
				if err != nil || !info.IsDir() {
					return count, fmt.Errorf("invalid original recovery directory: %v", err)
				}
				batch = filepath.Join(".recovered", rand.Text())
				if err := root.Mkdir(batch, 0700); err != nil {
					return count, err
				}
				if err := syncRoot(root, ".recovered"); err != nil {
					return count, err
				}
			}
			if err := root.Rename(key, filepath.Join(batch, key)); err != nil {
				return count, err
			}
			count++
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return count, readErr
			}
			break
		}
	}
	if batch != "" {
		return count, errors.Join(syncRoot(root, batch), syncRoot(root, "."))
	}
	return count, nil
}
