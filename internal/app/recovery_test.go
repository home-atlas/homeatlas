package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/database"
)

func TestSecondInstanceCannotCleanActiveWorkspace(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := NewWithOptions(ctx, db, nil, scheduleTestOrigin, dir, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	workspace := filepath.Join(dir, ".household-backup-active")
	if err = os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if second, err := NewWithOptions(ctx, db, nil, scheduleTestOrigin, dir, nil, Options{}); err == nil {
		second.Close()
		t.Fatal("second instance started")
	}
	if _, err = os.Stat(workspace); err != nil {
		t.Fatal("second instance cleaned live workspace", err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewWithOptions(ctx, db, nil, scheduleTestOrigin, dir, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err = os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatal("interrupted workspace retained", err)
	}
}
