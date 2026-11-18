package recovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTemporaryRecoveryIsConfinedAndPreservesBackups(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".household-backup-interrupted", "validated"), 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, ".household-backup-interrupted", "validated", "plaintext"))
	write(filepath.Join(outside, "sentinel"))
	if err := os.Symlink(outside, filepath.Join(dir, ".household-backup-symlink")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file-staging", "backups"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "file-staging", "original-partial"))
	write(filepath.Join(dir, "file-staging", "keep"))
	write(filepath.Join(dir, "backups", "previous.zip.age"))
	if err := Temporary(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, ".household-backup-interrupted"), filepath.Join(dir, ".household-backup-symlink"), filepath.Join(dir, "file-staging", "original-partial")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("temporary retained", path, err)
		}
	}
	for _, path := range []string{filepath.Join(outside, "sentinel"), filepath.Join(dir, "backups", "previous.zip.age"), filepath.Join(dir, "file-staging", "keep")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("unrelated file removed", path, err)
		}
	}
	if err := Temporary(context.Background(), dir); err != nil {
		t.Fatal("not idempotent", err)
	}
}

func TestOriginalRecoveryPreservesReferencesAndQuarantinesWithoutOverwrite(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	for _, key := range []string{"committed", "other-home", "damaged"} {
		if err := os.WriteFile(filepath.Join(dir, key), []byte(key), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 300 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("pending-%03d", i)), []byte("unfinished"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{"committed": true, "other-home": true}
	n, err := Originals(context.Background(), dir, refs)
	if err != nil || n != 301 {
		t.Fatal(n, err)
	}
	for key := range refs {
		if _, err := os.Stat(filepath.Join(dir, key)); err != nil {
			t.Fatal("reference moved", key, err)
		}
	}
	old, err := filepath.Glob(filepath.Join(dir, ".recovered", "*", "damaged"))
	if err != nil || len(old) != 1 {
		t.Fatal(old, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "damaged"), []byte("another version"), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := Originals(context.Background(), dir, refs); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	raw, err := os.ReadFile(old[0])
	if err != nil || string(raw) != "damaged" {
		t.Fatal("overwrote preserved original", err)
	}
	if n, err := Originals(context.Background(), dir, refs); err != nil || n != 0 {
		t.Fatal("not idempotent", n, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "link")); err != nil {
		t.Fatal("symlink touched", err)
	}
}

func TestRecoveryRejectsSymlinkWorkDirectories(t *testing.T) {
	for _, name := range []string{"file-staging", ".recovered"} {
		t.Run(name, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "orphan"), []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			if name == "file-staging" {
				err = Temporary(context.Background(), dir)
			} else {
				_, err = Originals(context.Background(), dir, nil)
			}
			if err == nil {
				t.Fatal("symlink directory accepted")
			}
			if _, err := os.Stat(filepath.Join(dir, "orphan")); err != nil {
				t.Fatal("original lost", err)
			}
		})
	}
}

func TestDataLockRejectsSecondInstanceAndReleases(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Acquire(dir); err == nil {
		second.Close()
		t.Fatal("second instance acquired data directory")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Close(); err != nil {
		t.Fatal("old close releases another lock", err)
	}
	if third, err := Acquire(dir); err == nil {
		third.Close()
		t.Fatal("stale close released new lock")
	}
}
