// Package recovery implements private filesystem maintenance before serving requests.
package recovery

import (
	"errors"
	"fmt"
	"os"
)

// Lock is released by the kernel even when the process is killed.
type Lock struct{ file *os.File }

func Acquire(directory string) (*Lock, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(".homeatlas.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("data directory is already in use or cannot be locked: %w", err)
	}
	return &Lock{f}, nil
}

func (l *Lock) Close() error {
	err := l.file.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
