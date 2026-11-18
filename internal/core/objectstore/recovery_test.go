package objectstore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUncommittedRemoteUploadsRecoverAndPublishedObjectsSurvive(t *testing.T) {
	s, server := fixture(t)
	ctx := context.Background()
	if err := s.Save(ctx, "home", config(server), allow); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(s.dir, "input")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw := []byte("private original")
	if _, err = f.Write(raw); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	for _, publish := range []bool{false, true} {
		key := fmt.Sprintf("original-%t", publish)
		pending, err := s.Upload(ctx, "home", "documents", key, f, int64(len(raw)), digest)
		if err != nil {
			t.Fatal(err)
		}
		var intents int
		if err = s.db.QueryRow("SELECT count(*) FROM file_storage_cleanup").Scan(&intents); err != nil || intents != 1 {
			t.Fatal("PUT missing durable intent", intents, err)
		}
		if publish {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = pending.Publish(ctx, tx); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			pending.Abort() // An ambiguous caller must never delete a committed locator.
			if _, err = s.Upload(ctx, "home", "documents", key, f, int64(len(raw)), digest); err == nil {
				t.Fatal("published original can be overwritten")
			}
		} else {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = pending.Publish(ctx, tx); err != nil {
				t.Fatal(err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			server.DenyDelete(true)
			pending.Abort()
			if err = s.RecoverPending(ctx, nil); err == nil {
				t.Fatal("failed cleanup not reported")
			}
			if server.Count() != 1 {
				t.Fatal("failed delete lost pending object")
			}
			server.DenyDelete(false)
		}
		restarted, err := New(ctx, s.db, s.dir)
		if err != nil {
			t.Fatal(err)
		}
		restarted.SetHTTPClient(server.Client())
		if err = restarted.RecoverPending(ctx, nil); err != nil {
			t.Fatal(err)
		}
		want := 0
		if publish {
			want = 1
		}
		if server.Count() != want {
			t.Fatal("incorrect object recovery", publish, server.Count())
		}
		if err = s.db.QueryRow("SELECT count(*) FROM file_storage_cleanup").Scan(&intents); err != nil || intents != 0 {
			t.Fatal("intent not completed", intents, err)
		}
	}
}

func TestReconcileRejectsSymlinkCollectionParent(t *testing.T) {
	s, _ := fixture(t)
	c, _ := seedOriginal(t, s, "home", "first")
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "documents"), 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "documents", "sentinel")
	if err := os.WriteFile(sentinel, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.dir, "plugin-files")); err != nil {
		t.Fatal(err)
	}
	c.Directory = filepath.Join(s.dir, "plugin-files", "documents")
	if err := s.Reconcile(context.Background(), []Collection{c}); err == nil {
		t.Fatal("symlink parent accepted")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("external file moved", err)
	}
}

func TestReconcilePreservesAllHouseholdsDisabledAndUnknownCollections(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	c, _ := seedOriginal(t, s, "home", "first")
	if _, err := s.db.Exec("INSERT INTO documents VALUES('second','other','other-key',1,'digest')"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Directory, "other-key"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Directory, "orphan"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(ctx, []Collection{c}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"key-first", "other-key"} {
		if _, err := os.Stat(filepath.Join(c.Directory, key)); err != nil {
			t.Fatal("household reference moved", key, err)
		}
	}
	orphans, _ := filepath.Glob(filepath.Join(c.Directory, ".recovered", "*", "orphan"))
	if len(orphans) != 1 {
		t.Fatal("orphan not preserved", orphans)
	}
	missing := Collection{ID: "missing", Table: "missing_table", Directory: t.TempDir()}
	if err := os.WriteFile(filepath.Join(missing.Directory, "preserve"), []byte("unknown schema"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(ctx, []Collection{missing}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(missing.Directory, "preserve")); err != nil {
		t.Fatal("missing schema treated as empty", err)
	}
}
