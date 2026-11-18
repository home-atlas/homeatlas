package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/database"
)

func TestDatabasePathPreservesCacheAdviceTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := DatabasePath(context.Background(), db)
	if err != nil {
		t.Fatalf("cache advice lost its source database: %q %v", got, err)
	}
	wantInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	gotInfo, err := os.Stat(got)
	if err != nil || !os.SameFile(wantInfo, gotInfo) {
		t.Fatalf("cache advice lost its source database: %q %v", got, err)
	}
}
