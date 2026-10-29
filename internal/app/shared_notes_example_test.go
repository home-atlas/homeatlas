//go:build !race

package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/plugins"
)

func TestPersistentPluginSharedNotesExample(t *testing.T) {
	dir := t.TempDir()
	wasm, archive := filepath.Join(dir, "notes.wasm"), filepath.Join(dir, "notes.hap")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", wasm, "./internal/core/plugins/testdata/shared-notes")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v %s", err, out)
	}
	cmd = exec.Command("python3", "scripts/package-plugin.py", "internal/core/plugins/testdata/shared-notes/manifest.json", wasm, archive)
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("package example: %v %s", err, out)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	install := func(data string) {
		t.Helper()
		_, err := plugins.Install(context.Background(), filepath.Join(data, "plugins"), archive, digest, []string{"ext-shared-notes.read", "ext-shared-notes.write", "ext-shared-notes.share", "ext-shared-notes.storage", "ext-shared-notes.storage.shared"})
		if err != nil {
			t.Fatal(err)
		}
	}
	f := newCoreFixture(t, true)
	f.app.Close()
	install(f.dataDir)
	reopenNotes(t, &f)
	defer func() { f.app.Close() }()
	action := func(f fixture, cookie *http.Cookie, view string, body map[string]string, status int) string {
		t.Helper()
		w := perform(f.app, "POST", "/api/ext-shared-notes/actions/"+view, body, cookie)
		if w.Code != status {
			t.Fatalf("%s: %d %s, want %d", view, w.Code, w.Body.String(), status)
		}
		if status != 200 {
			return ""
		}
		var result struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Text
	}
	note := func(value string) map[string]string {
		body := map[string]string{"key": "compras"}
		if value != "" {
			body["value"] = value
		}
		return body
	}
	bobState, bob := acceptMember(t, f, inviteMember(t, f, "example-bob@example.test", "member", []string{"ext-shared-notes.read", "ext-shared-notes.write", "ext-shared-notes.share"}, f.cookie))
	action(f, f.cookie, "private_save", note("Privada\nSegunda línea"), 200)
	action(f, f.cookie, "save", note("Pan"), 200)
	if got := action(f, bob, "private_read", note(""), 200); got != "No hay una nota accesible con esa clave." {
		t.Fatal("private leak", got)
	}
	if got := action(f, bob, "list", map[string]string{}, 200); got != "No hay notas accesibles." {
		t.Fatal("shared leak", got)
	}
	grant := map[string]string{"key": "compras", "user": bobState.Session.User.ID, "permission": "read"}
	action(f, f.cookie, "grant", grant, 200)
	if got := action(f, bob, "read", note(""), 200); got != "Pan" {
		t.Fatal(got)
	}
	action(f, bob, "save", note("Queso"), 403)
	action(f, bob, "grants", note(""), 403)
	if got := action(f, f.cookie, "grants", note(""), 200); got != bobState.Session.User.ID+": lectura" {
		t.Fatal(got)
	}
	grant["permission"] = "write"
	action(f, f.cookie, "grant", grant, 200)
	action(f, bob, "save", note("Queso"), 200)
	action(f, bob, "grant", grant, 403)
	copy := backupRequest(f, f.cookie)
	if copy.Code != 200 {
		t.Fatal(copy.Code, copy.Body.String())
	}
	action(f, f.cookie, "revoke", map[string]string{"key": "compras", "user": bobState.Session.User.ID}, 200)
	action(f, f.cookie, "private_save", note("Cambio"), 200)
	if w := restoreRequest(f, copy.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := action(f, bob, "read", note(""), 200); got != "No hay una nota accesible con esa clave." {
		t.Fatal("restored revoked grant", got)
	}
	if got := action(f, f.cookie, "private_read", note(""), 200); got != "Privada\nSegunda línea" {
		t.Fatal("private restore", got)
	}
	if got := action(f, f.cookie, "read", note(""), 200); got != "Queso" {
		t.Fatal("shared restore", got)
	}
	target := newCoreFixture(t, false)
	code, err := os.ReadFile(filepath.Join(target.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	target.app.Close()
	install(target.dataDir)
	reopenNotes(t, &target)
	defer target.app.Close()
	if w := recoverNewRequest(target, copy.Body.Bytes(), strings.TrimSpace(string(code)), origin); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	member := loginRecovered(t, target, "example-bob@example.test", invitedPassword)
	if got := action(target, member, "read", note(""), 200); got != "Queso" {
		t.Fatal("new recovery", got)
	}
	action(target, member, "save", note("Leche"), 200)
}
