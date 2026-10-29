package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/testsupport/s3test"
)

func fixture(t *testing.T) (*Store, *s3test.Server) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := New(context.Background(), db, dir)
	if err != nil {
		t.Fatal(err)
	}
	server := s3test.New(t)
	s.HTTPClient = server.Client()
	return s, server
}
func config(server *s3test.Server) Settings {
	return Settings{Provider: "s3", S3: S3Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "private-bucket", Prefix: "homeatlas", PathStyle: true, AccessKey: "fixture-access", SecretKey: "fixture-secret"}}
}
func allow(*sql.Tx) error { return nil }
func TestLocationsSurviveSwitchesRestartAndCorruption(t *testing.T) {
	s, server := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	local, err := s.Settings(ctx, "home")
	if err != nil || local.Provider != "local" {
		t.Fatal(local, err)
	}
	if err = s.Save(ctx, "home", config(server), allow); err != nil {
		t.Fatal(err)
	}
	if server.Count() != 0 {
		t.Fatal("probe leaked")
	}
	settings, err := s.Settings(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(settings)
	if bytes.Contains(raw, []byte("fixture-")) || !settings.CredentialsConfigured {
		t.Fatal("credentials exposed", string(raw))
	}
	var sealed []byte
	if err = s.db.QueryRow("SELECT sealed FROM file_storage_profiles").Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("fixture-secret")) {
		t.Fatal("credentials plaintext", err)
	}
	file, err := os.CreateTemp(s.dir, "input")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data := []byte("Original almacenado en S3")
	file.Write(data)
	hash := sha256.Sum256(data)
	pending, err := s.Upload(ctx, "home", "documents", "original", file, int64(len(data)), hex.EncodeToString(hash[:]))
	if err != nil || pending == nil {
		t.Fatal(err)
	}
	tx, _ := s.db.BeginTx(ctx, nil)
	if err = pending.Publish(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Backup export holds the only live DB connection; lookups must use that transaction.
	tx, _ = s.db.BeginTx(ctx, nil)
	f, cleanup, err := s.OpenFrom(ctx, tx, "documents", "original")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	name := f.Name()
	cleanup()
	tx.Rollback()
	if !bytes.Equal(got, data) {
		t.Fatal(string(got))
	}
	if _, err = os.Stat(name); !os.IsNotExist(err) {
		t.Fatal("temporary original retained")
	}
	settings, err = s.Settings(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Save(ctx, "home", settings, allow); err != nil {
		t.Fatal("retain credentials", err)
	}
	settings, err = s.Settings(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	settings.S3.AccessKey = "fixture-access"
	settings.S3.SecretKey = "fixture-secret-rotated"
	if err = s.Save(ctx, "home", settings, allow); err != nil {
		t.Fatal("rotate credentials", err)
	}
	_, oldProfile, err := s.profile(ctx, pending.profile)
	if err != nil || oldProfile.SecretKey != "fixture-secret-rotated" {
		t.Fatal("old original credentials not rotated", err)
	}
	settings, err = s.Settings(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Save(ctx, "home", Settings{Provider: "local", Revision: settings.Revision}, allow); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(ctx, s.db, s.dir)
	if err != nil {
		t.Fatal(err)
	}
	restarted.HTTPClient = server.Client()
	f, cleanup, err = restarted.Open(ctx, "documents", "original")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(f)
	cleanup()
	if !bytes.Equal(got, data) {
		t.Fatal("switch lost original")
	}
	if _, _, err = s.Open(ctx, "ext-other", "original"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cross collection locator", err)
	}
	other, err := s.Settings(ctx, "other-home")
	if err != nil || other.Provider != "local" {
		t.Fatal("household configuration leaked", err)
	}
	server.Corrupt()
	if _, _, err = s.Open(ctx, "documents", "original"); !errors.Is(err, Unavailable) {
		t.Fatal("corrupted original accepted", err)
	}
}
func TestSettingsFailuresConflictPermissionAndKeyBinding(t *testing.T) {
	s, server := fixture(t)
	ctx := context.Background()
	input := config(server)
	denied := errors.New("revoked owner")
	if err := s.Save(ctx, "home", input, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	got, _ := s.Settings(ctx, "home")
	if got.Provider != "local" || server.Count() != 0 {
		t.Fatal("failed save changed settings")
	}
	server.Deny(true)
	if err := s.Save(ctx, "home", input, allow); !errors.Is(err, Unavailable) {
		t.Fatal(err)
	}
	server.Deny(false)
	input.S3.Endpoint = "http://example.test"
	if err := s.Save(ctx, "home", input, allow); !errors.Is(err, Invalid) {
		t.Fatal("insecure endpoint", err)
	}
	input = config(server)
	if err := s.Save(ctx, "home", input, allow); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "home", Settings{Provider: "local"}, allow); !errors.Is(err, Conflict) {
		t.Fatal("stale settings", err)
	}
	got, _ = s.Settings(ctx, "home")
	got.S3.Bucket = "another-bucket"
	if err := s.Save(ctx, "home", got, allow); !errors.Is(err, Invalid) {
		t.Fatal("reused credentials at changed destination", err)
	}
	// Swapping ciphertext between profile identities must fail authentication.
	_, id, _ := s.current(ctx, "home")
	var sealed []byte
	s.db.QueryRow("SELECT sealed FROM file_storage_profiles WHERE id=?", id).Scan(&sealed)
	s.db.Exec("INSERT INTO file_storage_profiles VALUES('swapped','other',?)", sealed)
	if _, _, err := s.profile(ctx, "swapped"); !errors.Is(err, Unavailable) {
		t.Fatal("unbound encrypted credentials", err)
	}
	if err := os.Chmod(filepath.Join(s.dir, "file-storage.key"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settings(ctx, "home"); !errors.Is(err, Unavailable) {
		t.Fatal("public wrapping key", err)
	}
}
