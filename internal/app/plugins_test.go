package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/app"
	"github.com/home-atlas/homeatlas/internal/core/plugins"
)

func installTestPlugin(t *testing.T, dir string) {
	t.Helper()
	wasm, err := os.ReadFile("../core/plugins/testdata/ok.wasm")
	if err != nil {
		t.Fatal(err)
	}
	m := plugins.Manifest{ContractVersion: "1", ID: "ext-test", Name: "External Test", Version: "1.0.0", WasmSHA256: fmt.Sprintf("%x", sha256.Sum256(wasm)), Views: []plugins.View{{ID: "calculate", Title: "Calculate", Description: "Test", SubmitLabel: "Run", Fields: []plugins.Field{}}}}
	raw, _ := json.Marshal(m)
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("manifest.json")
	w.Write(raw)
	w, _ = z.Create("plugin.wasm")
	w.Write(wasm)
	z.Close()
	archive := filepath.Join(t.TempDir(), "test.hap")
	if err := os.WriteFile(archive, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := plugins.Install(context.Background(), filepath.Join(dir, "plugins"), archive, fmt.Sprintf("%x", sha256.Sum256(b.Bytes())), []string{"ext-test.read", "ext-test.write"}); err != nil {
		t.Fatal(err)
	}
}

func TestExternalPluginInstallationPermissionsRestartAndEncryptedRestore(t *testing.T) {
	f := newCoreFixture(t, true)
	f.app.Close()
	installTestPlugin(t, f.dataDir)
	reopen := func() {
		t.Helper()
		var err error
		f.app, err = app.NewWithModules(context.Background(), f.db, f.assets, origin, f.dataDir, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	reopen()
	defer func() { f.app.Close() }()
	if w := perform(f.app, "GET", "/api/modules", nil, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"runtime":"wasi-preview1"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := perform(f.app, "POST", "/api/ext-test/actions/calculate", map[string]string{}, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"ok"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	backup := backupRequest(f, f.cookie)
	if backup.Code != 200 || !bytes.HasPrefix(backup.Body.Bytes(), []byte("age-encryption.org/v1\n")) {
		t.Fatal("plugin broke encrypted backup", backup.Code, backup.Body.String())
	}
	if w := perform(f.app, "PATCH", "/api/household/modules/ext-test", map[string]bool{"enabled": false}, f.cookie); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := perform(f.app, "GET", "/api/ext-test", nil, f.cookie); w.Code != 404 {
		t.Fatal("disabled plugin accessible")
	}
	if w := restoreRequest(f, backup.Body.Bytes(), f.cookie, origin, "replace-household"); w.Code != 200 {
		t.Fatal("plugin restore", w.Code, w.Body.String())
	}
	if w := perform(f.app, "POST", "/api/ext-test/actions/calculate", map[string]string{}, f.cookie); w.Code != 200 {
		t.Fatal("restored plugin preference", w.Code, w.Body.String())
	}
	if _, err := f.db.Exec("DELETE FROM identity_permissions WHERE capability='ext-test.write'"); err != nil {
		t.Fatal(err)
	}
	f.app.Close()
	reopen()
	if w := perform(f.app, "POST", "/api/ext-test/actions/calculate", map[string]string{}, f.cookie); w.Code != 403 {
		t.Fatal("restart restored revoked permission", w.Code, w.Body.String())
	}
	f.app.Close()
	var err error
	f.app, err = app.NewWithOptions(context.Background(), f.db, f.assets, origin, f.dataDir, nil, app.Options{DisableExternalPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	if w := perform(f.app, "GET", "/api/ext-test", nil, f.cookie); w.Code != 404 {
		t.Fatal("omitted plugin mounted", w.Code)
	}
	if w := backupRequest(f, f.cookie); w.Code != 200 {
		t.Fatal("omitted package lost backup coverage", w.Code, w.Body.String())
	}
	assertNoDocuments(t, f)
}

func TestExternalPluginRecoveryRequiresApprovedPackage(t *testing.T) {
	source := newCoreFixture(t, true)
	source.app.Close()
	installTestPlugin(t, source.dataDir)
	var err error
	source.app, err = app.NewWithModules(context.Background(), source.db, source.assets, origin, source.dataDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer source.app.Close()
	archive := backupRequest(source, source.cookie)
	if archive.Code != 200 {
		t.Fatal(archive.Code, archive.Body.String())
	}
	target := newCoreFixture(t, false)
	code, err := os.ReadFile(filepath.Join(target.dataDir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	if w := recoverNewRequest(target, archive.Body.Bytes(), strings.TrimSpace(string(code)), origin); w.Code != 409 || !strings.Contains(w.Body.String(), "backup_module_required") {
		t.Fatal("missing package accepted", w.Code, w.Body.String())
	}
	target.app.Close()
	installTestPlugin(t, target.dataDir)
	target.app, err = app.NewWithModules(context.Background(), target.db, target.assets, origin, target.dataDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer target.app.Close()
	if w := recoverNewRequest(target, archive.Body.Bytes(), strings.TrimSpace(string(code)), origin); w.Code != 201 {
		t.Fatal("matching package recovery", w.Code, w.Body.String())
	}
}
