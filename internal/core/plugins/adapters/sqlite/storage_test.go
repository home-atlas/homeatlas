package sqlite

import (
	"context"
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
	if err != nil || got != path {
		t.Fatalf("cache advice lost its source database: %q %v", got, err)
	}
}
