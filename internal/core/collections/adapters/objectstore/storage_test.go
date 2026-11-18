package remotestorage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
)

func TestLocalStorageDoesNotPublishTypedNilOriginal(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := objectstore.New(context.Background(), db, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Local mode has no remote locator; passing a typed nil through the port would panic on Publish.
	pending, err := New(store).Upload(context.Background(), "home", "documents", "original", nil, 1, "digest")
	if err != nil || pending != nil {
		t.Fatalf("local upload returned a remote publication: %v %v", pending, err)
	}
}
