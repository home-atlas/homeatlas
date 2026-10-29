//go:build !race

// The native WASI compiler exceeds the production six-second cold budget under
// race instrumentation. Keep real Go-SDK tests in the normal suite; tiny WASI
// fixtures exercise the same storage/runtime boundaries in the race suite.
package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/plugins"
)

var notesBuild struct {
	sync.Once
	wasm []byte
	err  error
}

func installNotesPlugin(t *testing.T, dir string) {
	t.Helper()
	notesBuild.Do(func() {
		output := filepath.Join(t.TempDir(), "notes.wasm")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", output, "./internal/core/plugins/testdata/private-notes")
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0", "GOFLAGS=")
		if raw, err := cmd.CombinedOutput(); err != nil {
			notesBuild.err = fmt.Errorf("build real WASI example: %w: %s", err, raw)
			return
		}
		notesBuild.wasm, notesBuild.err = os.ReadFile(output)
	})
	if notesBuild.err != nil {
		t.Fatal(notesBuild.err)
	}
	raw, err := os.ReadFile("../../internal/core/plugins/testdata/private-notes/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.WasmSHA256 = fmt.Sprintf("%x", sha256.Sum256(notesBuild.wasm))
	raw, _ = json.Marshal(manifest)
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	w, _ := z.Create("manifest.json")
	w.Write(raw)
	w, _ = z.Create("plugin.wasm")
	w.Write(notesBuild.wasm)
	z.Close()
	path := filepath.Join(t.TempDir(), "notes.hap")
	if err := os.WriteFile(path, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	if _, err := plugins.Install(context.Background(), filepath.Join(dir, "plugins"), path, digest, []string{"ext-private-notes.read", "ext-private-notes.write"}); err == nil {
		t.Fatal("storage installed without explicit host API consent")
	}
	if _, err := plugins.Install(context.Background(), filepath.Join(dir, "plugins"), path, digest, []string{"ext-private-notes.read", "ext-private-notes.write", "ext-private-notes.storage"}); err != nil {
		t.Fatal(err)
	}
}
func reopenNotes(t *testing.T, f *fixture) {
	t.Helper()
	var err error
	f.app, err = app.NewWithModules(context.Background(), f.db, f.assets, origin, f.dataDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}
func noteAction(t *testing.T, f fixture, cookie *http.Cookie, view string, input map[string]string) string {
	t.Helper()
	w := perform(f.app, "POST", "/api/ext-private-notes/actions/"+view, input, cookie)
	if w.Code != 200 {
		t.Fatalf("notes %s: %d %s", view, w.Code, w.Body.String())
	}
	var result plugins.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Text
}
func assertNote(t *testing.T, f fixture, cookie *http.Cookie, key, value string) {
	t.Helper()
	if got := noteAction(t, f, cookie, "read", map[string]string{"key": key}); got != value {
		t.Fatalf("private note: got %q want %q", got, value)
	}
}

func TestPersistentPluginHTTPIsolationPermissionsRestartAndRestore(t *testing.T) {
	f := newCoreFixture(t, true)
	coreArchive := backupRequest(f, f.cookie)
	if coreArchive.Code != 200 {
		t.Fatal(coreArchive.Code)
	}
	f.app.Close()
	installNotesPlugin(t, f.dataDir)
	reopenNotes(t, &f)
	defer func() { f.app.Close() }()
	permissions := []string{"ext-private-notes.read", "ext-private-notes.write"}
	memberState, bob := acceptMember(t, f, inviteMember(t, f, "plugin-bob@example.test", "member", permissions, f.cookie))
	_, reader := acceptMember(t, f, inviteMember(t, f, "plugin-reader@example.test", "restricted", permissions[:1], f.cookie))
	noteAction(t, f, f.cookie, "save", map[string]string{"key": "same", "value": "Owner private note"})
	assertNote(t, f, bob, "same", "No hay una nota con esa clave.")
	noteAction(t, f, bob, "save", map[string]string{"key": "same", "value": "Bob private note"})
	assertNote(t, f, f.cookie, "same", "Owner private note")
	assertNote(t, f, reader, "same", "No hay una nota con esa clave.")
	if w := perform(f.app, "POST", "/api/ext-private-notes/actions/save", map[string]string{"key": "same", "value": "forbidden"}, reader); w.Code != 403 {
		t.Fatal("read-only account wrote", w.Code)
	}
	if w := perform(f.app, "POST", "/api/ext-private-notes/actions/read", map[string]string{"key": "same", "user_id": f.state.Session.User.ID}, bob); w.Code != 400 {
		t.Fatal("guest selected another user", w.Code)
	}
	f.app.Close()
	reopenNotes(t, &f)
	assertNote(t, f, bob, "same", "Bob private note")
	// Restoring an earlier Core-only archive preserves a module added afterward.
	if w := restoreRequest(f, coreArchive.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	assertNote(t, f, f.cookie, "same", "Owner private note")
	archive := backupRequest(f, f.cookie)
	if archive.Code != 200 || !bytes.HasPrefix(archive.Body.Bytes(), []byte("age-encryption.org/v1\n")) {
		t.Fatal("encrypted plugin backup", archive.Code, archive.Body.String())
	}
	if bytes.Contains(archive.Body.Bytes(), []byte("Bob private note")) {
		t.Fatal("plaintext private records in encrypted output")
	}
	noteAction(t, f, f.cookie, "save", map[string]string{"key": "same", "value": "Changed after backup"})
	noteAction(t, f, bob, "delete", map[string]string{"key": "same"})
	if _, err := f.db.Exec("DELETE FROM identity_permissions WHERE user_id=? AND capability='ext-private-notes.write'", memberState.Session.User.ID); err != nil {
		t.Fatal(err)
	}
	if w := restoreRequest(f, archive.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal("persistent restore", w.Code, w.Body.String())
	}
	assertNote(t, f, f.cookie, "same", "Owner private note")
	assertNote(t, f, bob, "same", "Bob private note")
	if w := perform(f.app, "POST", "/api/ext-private-notes/actions/save", map[string]string{"key": "same", "value": "revoked"}, bob); w.Code != 403 {
		t.Fatal("restore regranted write", w.Code)
	}
	noteAction(t, f, f.cookie, "save", map[string]string{"key": "same", "value": "Retain on failed restore"})
	if _, err := f.db.Exec(`CREATE TRIGGER fail_plugin_restore BEFORE INSERT ON plugin_storage_records BEGIN SELECT RAISE(ABORT,'test restore failure'); END`); err != nil {
		t.Fatal(err)
	}
	if w := restoreRequest(f, archive.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 503 {
		t.Fatal("restore failure ignored", w.Code, w.Body.String())
	}
	if _, err := f.db.Exec("DROP TRIGGER fail_plugin_restore"); err != nil {
		t.Fatal(err)
	}
	assertNote(t, f, f.cookie, "same", "Retain on failed restore")
	assertNote(t, f, bob, "same", "Bob private note")
	if w := perform(f.app, "DELETE", "/api/household/members/"+memberState.Session.User.ID, nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, newBob := acceptMember(t, f, inviteMember(t, f, "plugin-bob@example.test", "member", permissions, f.cookie))
	assertNote(t, f, newBob, "same", "No hay una nota con esa clave.")
	if w := restoreRequest(f, archive.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	assertNote(t, f, newBob, "same", "No hay una nota con esa clave.")
	f.app.Close()
	var err error
	f.app, err = app.NewWithOptions(context.Background(), f.db, f.assets, origin, f.dataDir, nil, app.Options{DisableExternalPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := backupRequest(f, f.cookie); w.Code != 200 {
		t.Fatal("dormant persistent plugin backup", w.Code, w.Body.String())
	}
	assertNoDocuments(t, f)
}

func TestPersistentPluginNewInstallationRecoveryRemapsPrivateNamespaces(t *testing.T) {
	source := newCoreFixture(t, true)
	source.app.Close()
	installNotesPlugin(t, source.dataDir)
	reopenNotes(t, &source)
	defer source.app.Close()
	_, bob := acceptMember(t, source, inviteMember(t, source, "plugin-recovery@example.test", "member", []string{"ext-private-notes.read", "ext-private-notes.write"}, source.cookie))
	noteAction(t, source, source.cookie, "save", map[string]string{"key": "same", "value": "Recovered owner data"})
	noteAction(t, source, bob, "save", map[string]string{"key": "same", "value": "Recovered member data"})
	archive := backupRequest(source, source.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	target := newCoreFixture(t, false)
	code, err := os.ReadFile(filepath.Join(target.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	if w := recoverNewRequest(target, archive.Body.Bytes(), strings.TrimSpace(string(code)), origin); w.Code != 409 {
		t.Fatal("missing package accepted", w.Code, w.Body.String())
	}
	target.app.Close()
	installNotesPlugin(t, target.dataDir)
	reopenNotes(t, &target)
	defer target.app.Close()
	if w := recoverNewRequest(target, archive.Body.Bytes(), strings.TrimSpace(string(code)), origin); w.Code != 201 {
		t.Fatal("recover persistent data", w.Code, w.Body.String())
	}
	w := perform(target.app, "POST", "/api/auth/login", map[string]string{"identifier": "core-owner", "password": invitedPassword}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	owner := w.Result().Cookies()[0]
	member := loginRecovered(t, target, "plugin-recovery@example.test", invitedPassword)
	assertNote(t, target, owner, "same", "Recovered owner data")
	assertNote(t, target, member, "same", "Recovered member data")
	var bad int
	if err := target.db.QueryRow(`SELECT count(*) FROM plugin_storage_records r JOIN plugin_storage_scopes s USING(plugin_id,household_id,member_id) LEFT JOIN identity_member_ids m ON m.id=s.member_id AND m.user_id=s.user_id AND m.household_id=s.household_id WHERE m.id IS NULL`).Scan(&bad); err != nil || bad != 0 {
		t.Fatal("recovery retained historical membership", bad, err)
	}
}
