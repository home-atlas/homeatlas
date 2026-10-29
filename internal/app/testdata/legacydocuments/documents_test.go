package documents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
)

func TestMigrationPreservesLegacyRecordsAndSearch(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := New(db, filepath.Join(dir, "documents"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	steps := m.Migrations()
	if err := database.Apply(ctx, db, "documents", steps[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO documents(id,household_id,owner_id,title) VALUES('old','home','alice','Legacy receipt')`); err != nil {
		t.Fatal(err)
	}
	if err := database.Apply(ctx, db, "documents", steps); err != nil {
		t.Fatal(err)
	}
	d, err := scanDocument(db.QueryRow("SELECT " + columns + " FROM documents d WHERE id='old'"))
	if err != nil {
		t.Fatal(err)
	}
	if d.HasFile || d.Title != "Legacy receipt" || len(d.Tags) != 0 {
		t.Fatalf("lost legacy metadata: %+v", d)
	}
	ctx = access.WithPrincipal(ctx, access.Principal{UserID: "alice", HouseholdID: "home", Permissions: map[string]bool{Read: true}})
	result, err := m.Search(ctx, "Legacy")
	if err != nil || len(result) != 1 || result[0].Path != "/documents/old" {
		t.Fatalf("rebuild: %+v %v", result, err)
	}
	if err := database.Apply(ctx, db, "documents", steps); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataAndFileNames(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"title":"a","category":"otros","household_id":"other"}`, `{"title":"a\u0000","category":"otros"}`, `{"title":"a","category":"otros","tags":["` + strings.Repeat("a", 41) + `"]}`} {
		if _, err := parseMetadata([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, name := range []string{"../secret.txt", `..\secret.txt`, "x\n.txt", "x:stream.txt", strings.Repeat("a", 256) + ".txt"} {
		if safeFilename(name) {
			t.Errorf("unsafe %q", name)
		}
	}
	// Reject truncated image headers instead of trusting the extension.
	f, err := os.CreateTemp(t.TempDir(), "huge.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write([]byte("\x89PNG\r\n\x1a\ninvalid"))
	if _, err := fileType(f, "huge.png", 19); err == nil {
		t.Fatal("invalid image header accepted")
	}
}
