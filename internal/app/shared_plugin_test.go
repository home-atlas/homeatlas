//go:build !race

// Real Go/WASI compilation is tested without instrumenting the native compiler;
// small guest fixtures cover the same host boundaries in the race suite.
package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/home-atlas/homeatlas/internal/core/plugins"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var sharedBuild struct {
	sync.Once
	wasm []byte
	err  error
}

func installSharedPlugin(t *testing.T, dir string) {
	t.Helper()
	sharedBuild.Do(func() {
		output := filepath.Join(t.TempDir(), "shared.wasm")
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", output, "./internal/core/plugins/testdata/shared-load")
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0", "GOFLAGS=")
		if raw, err := cmd.CombinedOutput(); err != nil {
			sharedBuild.err = fmt.Errorf("compile shared SDK fixture: %w %s", err, raw)
			return
		}
		sharedBuild.wasm, sharedBuild.err = os.ReadFile(output)
	})
	if sharedBuild.err != nil {
		t.Fatal(sharedBuild.err)
	}
	raw, err := os.ReadFile("../core/plugins/testdata/shared-load/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m plugins.Manifest
	if json.Unmarshal(raw, &m) != nil {
		t.Fatal("invalid fixture manifest")
	}
	m.WasmSHA256 = fmt.Sprintf("%x", sha256.Sum256(sharedBuild.wasm))
	raw, _ = json.Marshal(m)
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	w, _ := z.Create("manifest.json")
	w.Write(raw)
	w, _ = z.Create("plugin.wasm")
	w.Write(sharedBuild.wasm)
	z.Close()
	path := filepath.Join(t.TempDir(), "shared.hap")
	os.WriteFile(path, out.Bytes(), 0600)
	digest := fmt.Sprintf("%x", sha256.Sum256(out.Bytes()))
	if _, err := plugins.Install(context.Background(), filepath.Join(dir, "plugins"), path, digest, []string{"ext-shared-load.read", "ext-shared-load.write", "ext-shared-load.storage"}); err == nil {
		t.Fatal("shared API installed without consent")
	}
	if _, err := plugins.Install(context.Background(), filepath.Join(dir, "plugins"), path, digest, []string{"ext-shared-load.read", "ext-shared-load.write", "ext-shared-load.share", "ext-shared-load.storage", "ext-shared-load.storage.shared"}); err != nil {
		t.Fatal(err)
	}
}
func sharedInput(seed int) map[string]string {
	return map[string]string{"start": "0", "count": "1", "bytes": "256", "seed": fmt.Sprint(seed)}
}
func sharedHTTP(t *testing.T, f fixture, cookie *http.Cookie, view string, body map[string]string, status int) {
	t.Helper()
	w := perform(f.app, "POST", "/api/ext-shared-load/actions/"+view, body, cookie)
	if w.Code != status {
		t.Fatalf("shared %s: %d %s (want %d)", view, w.Code, w.Body.String(), status)
	}
}
func sharedHTTPGrant(t *testing.T, f fixture, cookie *http.Cookie, user, permission string) {
	t.Helper()
	body := sharedInput(1)
	body["user"] = user
	body["permission"] = permission
	sharedHTTP(t, f, cookie, "grant", body, 200)
}
func TestPersistentPluginSharedPermissionsAndRecovery(t *testing.T) {
	f := newCoreFixture(t, true)
	f.app.Close()
	installSharedPlugin(t, f.dataDir)
	reopenNotes(t, &f)
	defer func() { f.app.Close() }()
	caps := []string{"ext-shared-load.read", "ext-shared-load.write", "ext-shared-load.share"}
	bobState, bob := acceptMember(t, f, inviteMember(t, f, "shared-bob@example.test", "member", caps, f.cookie))
	sharedHTTP(t, f, f.cookie, "shared_write", sharedInput(7), 200)
	sharedHTTP(t, f, bob, "shared_missing", sharedInput(7), 200)
	sharedHTTPGrant(t, f, f.cookie, bobState.Session.User.ID, "read")
	sharedHTTP(t, f, bob, "shared_check", sharedInput(7), 200)
	sharedHTTP(t, f, bob, "shared_write", sharedInput(8), 403)
	sharedHTTPGrant(t, f, f.cookie, bobState.Session.User.ID, "write")
	sharedHTTP(t, f, bob, "shared_write", sharedInput(8), 200)
	sharedHTTP(t, f, f.cookie, "shared_check", sharedInput(8), 200)
	body := sharedInput(1)
	body["user"] = f.state.Session.User.ID
	body["permission"] = "read"
	sharedHTTP(t, f, bob, "grant", body, 403)
	archive := backupRequest(f, f.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	body = sharedInput(1)
	body["user"] = bobState.Session.User.ID
	sharedHTTP(t, f, f.cookie, "revoke", body, 200)
	if w := restoreRequest(f, archive.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	sharedHTTP(t, f, bob, "shared_missing", sharedInput(8), 200)
	sharedHTTPGrant(t, f, f.cookie, bobState.Session.User.ID, "read")
	if w := restoreRequest(f, archive.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	sharedHTTP(t, f, bob, "shared_check", sharedInput(8), 200)
	sharedHTTP(t, f, bob, "shared_write", sharedInput(9), 403)
	if w := perform(f.app, "DELETE", "/api/household/members/"+bobState.Session.User.ID, nil, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	bobState, bob = acceptMember(t, f, inviteMember(t, f, "shared-bob@example.test", "member", caps, f.cookie))
	if w := restoreRequest(f, archive.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	sharedHTTP(t, f, bob, "shared_missing", sharedInput(8), 200)
	sharedHTTPGrant(t, f, f.cookie, bobState.Session.User.ID, "write")
	archive = backupRequest(f, f.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	target := newCoreFixture(t, false)
	code, err := os.ReadFile(filepath.Join(target.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	if w := recoverNewRequest(target, archive.Body.Bytes(), strings.TrimSpace(string(code)), origin); w.Code != 409 {
		t.Fatal("missing plugin accepted", w.Code, w.Body.String())
	}
	target.app.Close()
	installSharedPlugin(t, target.dataDir)
	reopenNotes(t, &target)
	defer target.app.Close()
	if w := recoverNewRequest(target, archive.Body.Bytes(), strings.TrimSpace(string(code)), origin); w.Code != 201 {
		t.Fatal("shared recovery", w.Code, w.Body.String())
	}
	w := perform(target.app, "POST", "/api/auth/login", map[string]string{"identifier": "core-owner", "password": invitedPassword}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	owner := w.Result().Cookies()[0]
	member := loginRecovered(t, target, "shared-bob@example.test", invitedPassword)
	sharedHTTP(t, target, owner, "shared_check", sharedInput(8), 200)
	sharedHTTP(t, target, member, "shared_check", sharedInput(8), 200)
	sharedHTTP(t, target, member, "shared_write", sharedInput(9), 200)
	sharedHTTP(t, target, owner, "shared_check", sharedInput(9), 200)
}
