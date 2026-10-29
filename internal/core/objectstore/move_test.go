package objectstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func seedOriginal(t *testing.T, s *Store, house, id string) (Collection, string) {
	t.Helper()
	dir := filepath.Join(s.dir, "documents")
	os.MkdirAll(dir, 0700)
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS documents(id TEXT PRIMARY KEY,household_id TEXT NOT NULL,file_key TEXT NOT NULL,size_bytes INTEGER NOT NULL,sha256 TEXT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	data := "Original privado " + id
	key := "key-" + id
	if err = os.WriteFile(filepath.Join(dir, key), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("INSERT INTO documents VALUES(?,?,?,?,?)", id, house, key, len(data), fmt.Sprintf("%x", sha256.Sum256([]byte(data)))); err != nil {
		t.Fatal(err)
	}
	return Collection{ID: "documents", Table: "documents", Directory: dir}, data
}
func TestMoveRoundTripResumeAndScope(t *testing.T) {
	s, server := fixture(t)
	ctx := context.Background()
	c, data := seedOriginal(t, s, "home", "first")
	seedOriginal(t, s, "other", "foreign")
	if err := s.Save(ctx, "home", config(server), allow); err != nil {
		t.Fatal(err)
	}
	stats, err := s.MovePreview(ctx, "home", []Collection{c})
	if err != nil || stats.Local != 1 || stats.Remote != 0 {
		t.Fatal(stats, err)
	}
	result, err := s.MoveNext(ctx, "home", stats.Revision, []Collection{c}, allow)
	if err != nil || !result.Moved || result.Local != 0 || result.Remote != 1 || result.PendingCleanup != 0 {
		t.Fatal(result, err)
	}
	if _, err = os.Stat(filepath.Join(c.Directory, "key-first")); !os.IsNotExist(err) {
		t.Fatal("source local not removed", err)
	}
	if _, err = os.Stat(filepath.Join(c.Directory, "key-foreign")); err != nil {
		t.Fatal("foreign original touched", err)
	}
	var key string
	s.db.QueryRow("SELECT file_key FROM documents WHERE id='first'").Scan(&key)
	f, cleanup, err := s.Open(ctx, c.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(f)
	cleanup()
	if string(raw) != data {
		t.Fatal("remote content changed")
	}
	// Repeating after an interrupted browser is a no-op, not a second upload.
	result, err = s.MoveNext(ctx, "home", stats.Revision, []Collection{c}, allow)
	if err != nil || result.Moved || server.Count() != 1 {
		t.Fatal("duplicate move", result, err)
	}
	settings, _ := s.Settings(ctx, "home")
	settings.Provider = "local"
	if err = s.Save(ctx, "home", settings, allow); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Settings(ctx, "home")
	result, err = s.MoveNext(ctx, "home", current.Revision, []Collection{c}, allow)
	if err != nil || !result.Moved || result.Local != 1 || result.Remote != 0 || server.Count() != 0 {
		t.Fatal("reverse move", result, err)
	}
	s.db.QueryRow("SELECT file_key FROM documents WHERE id='first'").Scan(&key)
	raw, err = os.ReadFile(filepath.Join(c.Directory, key))
	if err != nil || string(raw) != data {
		t.Fatal("local destination", err, string(raw))
	}
}
func TestMoveFailureAndRevocationKeepSource(t *testing.T) {
	s, server := fixture(t)
	ctx := context.Background()
	c, data := seedOriginal(t, s, "home", "first")
	if err := s.Save(ctx, "home", config(server), allow); err != nil {
		t.Fatal(err)
	}
	stats, _ := s.MovePreview(ctx, "home", []Collection{c})
	server.Deny(true)
	if _, err := s.MoveNext(ctx, "home", stats.Revision, []Collection{c}, allow); err == nil {
		t.Fatal("failed destination accepted")
	}
	server.Deny(false)
	if raw, err := os.ReadFile(filepath.Join(c.Directory, "key-first")); err != nil || string(raw) != data {
		t.Fatal("failed copy lost source", err)
	}
	checks := 0
	denied := fmt.Errorf("revoked")
	_, err := s.MoveNext(ctx, "home", stats.Revision, []Collection{c}, func(tx *sql.Tx) error {
		checks++
		if tx != nil {
			return denied
		}
		return nil
	})
	if err != denied || checks != 2 || server.Count() != 0 {
		t.Fatal("revocation rollback", err, checks, server.Count())
	}
	var key string
	s.db.QueryRow("SELECT file_key FROM documents WHERE id='first'").Scan(&key)
	if key != "key-first" {
		t.Fatal("revoked move published")
	}
	if _, err := s.MoveNext(ctx, "home", "stale", []Collection{c}, allow); err != Conflict {
		t.Fatal("stale destination", err)
	}
	os.WriteFile(filepath.Join(c.Directory, "key-first"), []byte("corrupt"), 0600)
	if _, err := s.MoveNext(ctx, "home", stats.Revision, []Collection{c}, allow); err == nil {
		t.Fatal("corrupt source moved")
	}
	if server.Count() != 0 {
		t.Fatal("corrupt source uploaded")
	}
}

func TestMoveVerifiesDestinationAndRetriesCleanupAfterRestart(t *testing.T) {
	s, server := fixture(t)
	ctx := context.Background()
	c, _ := seedOriginal(t, s, "home", "first")
	if err := s.Save(ctx, "home", config(server), allow); err != nil {
		t.Fatal(err)
	}
	stats, _ := s.MovePreview(ctx, "home", []Collection{c})
	server.CorruptPut(true)
	if _, err := s.MoveNext(ctx, "home", stats.Revision, []Collection{c}, allow); err == nil {
		t.Fatal("corrupt destination published")
	}
	server.CorruptPut(false)
	var key string
	s.db.QueryRow("SELECT file_key FROM documents WHERE id='first'").Scan(&key)
	if key != "key-first" || server.Count() != 0 {
		t.Fatal("corrupt copy lost source")
	}
	result, err := s.MoveNext(ctx, "home", stats.Revision, []Collection{c}, allow)
	if err != nil || !result.Moved {
		t.Fatal(err)
	}
	settings, _ := s.Settings(ctx, "home")
	settings.Provider = "local"
	if err = s.Save(ctx, "home", settings, allow); err != nil {
		t.Fatal(err)
	}
	settings, _ = s.Settings(ctx, "home")
	server.DenyDelete(true)
	result, err = s.MoveNext(ctx, "home", settings.Revision, []Collection{c}, allow)
	if err != nil || !result.Moved || result.PendingCleanup != 1 || result.Local != 1 || result.Remote != 0 || server.Count() != 1 {
		t.Fatal("cleanup tracking", result, err)
	}
	s.db.QueryRow("SELECT file_key FROM documents WHERE id='first'").Scan(&key)
	if _, err = os.Stat(filepath.Join(c.Directory, key)); err != nil {
		t.Fatal("confirmed copy missing", err)
	}
	restarted, err := New(ctx, s.db, s.dir)
	if err != nil {
		t.Fatal(err)
	}
	restarted.HTTPClient = server.Client()
	server.DenyDelete(false)
	result, err = restarted.MoveNext(ctx, "home", settings.Revision, []Collection{c}, allow)
	if err != nil || result.Moved || result.PendingCleanup != 0 || server.Count() != 0 {
		t.Fatal("cleanup retry", result, err)
	}
}
