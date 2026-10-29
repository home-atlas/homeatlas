package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

func uleb(n int) []byte {
	var b []byte
	for {
		v := byte(n & 127)
		n >>= 7
		if n != 0 {
			v |= 128
		}
		b = append(b, v)
		if n == 0 {
			return b
		}
	}
}
func wasmString(s string) []byte { return append(uleb(len(s)), []byte(s)...) }
func section(id byte, data []byte) []byte {
	return append(append([]byte{id}, uleb(len(data))...), data...)
}

// A tiny WASI command keeps security/runtime tests independent of toolchains.
func command(output string, loop bool) []byte {
	b := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	b = append(b, section(1, []byte{2, 0x60, 4, 0x7f, 0x7f, 0x7f, 0x7f, 1, 0x7f, 0x60, 0, 0})...)
	imp := append([]byte{1}, wasmString("wasi_snapshot_preview1")...)
	imp = append(imp, wasmString("fd_write")...)
	imp = append(imp, 0, 0)
	b = append(b, section(2, imp)...)
	b = append(b, section(3, []byte{1, 1})...)
	b = append(b, section(5, []byte{1, 0, 1})...)
	exp := append([]byte{2}, wasmString("memory")...)
	exp = append(exp, 2, 0)
	exp = append(exp, wasmString("_start")...)
	exp = append(exp, 0, 1)
	b = append(b, section(7, exp)...)
	code := []byte{0, 0x41, 0, 0x41, 32, 0x36, 2, 0, 0x41, 4, 0x41, byte(len(output)), 0x36, 2, 0, 0x41, 1, 0x41, 0, 0x41, 1, 0x41, 8, 0x10, 0, 0x1a, 0x0b}
	if len(output) > 63 {
		panic("fixture string too large for signed one-byte LEB")
	}
	if loop {
		code = []byte{0, 0x03, 0x40, 0x0c, 0, 0x0b, 0x0b}
	}
	code = append(append([]byte{1}, uleb(len(code))...), code...)
	b = append(b, section(10, code)...)
	data := []byte{1, 0, 0x41, 32, 0x0b}
	data = append(data, uleb(len(output))...)
	data = append(data, []byte(output)...)
	return append(b, section(11, data)...)
}
func packageBytes(t *testing.T, wasm []byte, mutate func(*Manifest), extra string) []byte {
	t.Helper()
	m := Manifest{ContractVersion: "1", ID: "ext-test", Name: "Test", Version: "1.0.0", WasmSHA256: fmt.Sprintf("%x", sha256.Sum256(wasm)), Views: []View{{ID: "calculate", Title: "Calculate", Description: "Test", SubmitLabel: "Run", Fields: []Field{{Name: "value", Label: "Value", Type: "number", Required: true}}}}}
	if mutate != nil {
		mutate(&m)
	}
	raw, _ := json.Marshal(m)
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, f := range []struct {
		name string
		body []byte
	}{{"manifest.json", raw}, {"plugin.wasm", wasm}} {
		w, err := z.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(f.body)
	}
	if extra != "" {
		w, _ := z.Create(extra)
		w.Write([]byte("bad"))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func testPackage(t *testing.T, loop bool) *Package {
	t.Helper()
	p, err := Read(packageBytes(t, command(`{"text":"ok"}`, loop), nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPackageInstallApprovalIntegrityAndRestart(t *testing.T) {
	dir := t.TempDir()
	raw := packageBytes(t, command(`{"text":"ok"}`, false), nil, "")
	archive := filepath.Join(dir, "test.hap")
	os.WriteFile(archive, raw, 0600)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	installed := filepath.Join(dir, "plugins")
	for _, tc := range []struct {
		digest string
		allow  []string
	}{{strings.Repeat("0", 64), []string{"ext-test.read", "ext-test.write"}}, {digest, []string{"ext-test.read"}}, {digest, []string{"household.manage", "ext-test.read", "ext-test.write"}}} {
		if _, err := Install(context.Background(), installed, archive, tc.digest, tc.allow); err == nil {
			t.Fatal("unapproved/corrupt package installed")
		}
	}
	p, err := Install(context.Background(), installed, archive, digest, []string{"ext-test.read", "ext-test.write"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), installed, archive, digest, p.Permissions()); err == nil {
		t.Fatal("existing plugin replaced")
	}
	loaded, err := Load(installed)
	if err != nil || len(loaded) != 1 || loaded[0].Digest != digest {
		t.Fatal("restart load", err)
	}
	for _, name := range []string{"package.zip", "approval.json"} {
		info, err := os.Stat(filepath.Join(installed, p.Manifest.ID, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private file permissions", err)
		}
	}
	os.WriteFile(filepath.Join(installed, p.Manifest.ID, "approval.json"), []byte(`{"sha256":"wrong","permissions":[]}`), 0600)
	if _, err := Load(installed); err == nil {
		t.Fatal("tampered approval accepted")
	}
}

func TestPackageRejectsPathsLimitsAndIncompatibleContracts(t *testing.T) {
	wasm := command(`{"text":"ok"}`, false)
	for _, mutate := range []func(*Manifest){func(m *Manifest) { m.ID = "auth" }, func(m *Manifest) { m.ContractVersion = "2" }, func(m *Manifest) { m.Storage = "household" }, func(m *Manifest) { m.WasmSHA256 = strings.Repeat("0", 64) }, func(m *Manifest) { m.Views[0].Fields[0].Type = "html" }, func(m *Manifest) { m.Views = append(m.Views, m.Views[0]) }} {
		if _, err := Read(packageBytes(t, wasm, mutate, "")); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	for _, path := range []string{"../escape", "/absolute", "plugin.js"} {
		if _, err := Read(packageBytes(t, wasm, nil, path)); err == nil {
			t.Fatal("unexpected file accepted")
		}
	}
	if _, err := Read(make([]byte, MaxPackageBytes+1)); err == nil {
		t.Fatal("oversized package accepted")
	}
	if _, err := Read(packageBytes(t, make([]byte, maxWasmBytes+1), nil, "")); err == nil {
		t.Fatal("zip bomb entry accepted")
	}
}

func TestRuntimeDeadlineBoundedOutputAdmissionAndWarmReuse(t *testing.T) {
	e := &Engine{}
	defer e.Close()
	p := testPackage(t, false)
	for range 2 {
		r, err := e.Run(context.Background(), p, "calculate", map[string]string{"value": "4"})
		if err != nil || r.Text != "ok" {
			t.Fatal(r, err)
		}
	}
	compiled := e.compiled
	if _, err := e.Run(context.Background(), p, "calculate", nil); err != nil || e.compiled != compiled {
		t.Fatal("compiled module not reused", err)
	}
	e.mu.Lock()
	_, err := e.Run(context.Background(), p, "calculate", nil)
	e.mu.Unlock()
	if !errors.Is(err, ErrBusy) {
		t.Fatal("unbounded concurrent admission")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := e.Run(ctx, testPackage(t, true), "calculate", nil); err == nil || time.Since(started) > time.Second {
		t.Fatal("infinite guest was not stopped", err)
	}
	if _, err := e.Run(context.Background(), p, "calculate", nil); err != nil {
		t.Fatal("runtime failed after timeout", err)
	}
	resume := e.Pause()
	if e.runtime != nil || e.compiled != nil {
		t.Fatal("heavy operation retained compiled plugin")
	}
	if _, err := e.Run(context.Background(), p, "calculate", nil); !errors.Is(err, ErrBusy) {
		t.Fatal("plugin ran alongside heavy Core operation")
	}
	resume()
	if _, err := e.Run(context.Background(), p, "calculate", nil); err != nil {
		t.Fatal("plugin failed after heavy operation", err)
	}
	var out boundedOutput
	if _, err := out.Write(make([]byte, maxOutput+1)); err == nil || out.Len() != 0 {
		t.Fatal("unbounded output")
	}
	bad := testPackage(t, false)
	bad.wasm = command(`{"text":"ok","html":"bad"}`, false)
	bad.Digest = "other"
	if _, err := e.Run(context.Background(), bad, "calculate", nil); err == nil {
		t.Fatal("unsupported guest output accepted")
	}
}

func TestWasmValidationRejectsUnknownHostsAndOversizedMemory(t *testing.T) {
	if err := validateWasm(context.Background(), []byte("not wasm")); err == nil {
		t.Fatal("invalid wasm accepted")
	}
	b := command(`{"text":"ok"}`, false)
	b = bytes.Replace(b, []byte("wasi_snapshot_preview1"), []byte("evil_snapshot_preview1"), 1)
	if err := validateWasm(context.Background(), b); err == nil {
		t.Fatal("unknown host imports accepted")
	}
	b = command(`{"text":"ok"}`, false)
	b = bytes.Replace(b, section(5, []byte{1, 0, 1}), section(5, []byte{1, 0, 0x81, 4}), 1) // minimum 513 pages
	if err := validateWasm(context.Background(), b); err == nil {
		t.Fatal("guest memory limit ignored")
	}
}

func TestModuleAuthorizationActivationAndInput(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, err := modules.New(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{}
	defer e.Close()
	if err := r.Register(context.Background(), &Module{testPackage(t, false), e}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	r.Mount(mux)
	for _, tc := range []struct {
		permissions map[string]bool
		body        string
		status      int
	}{{nil, `{"value":"2"}`, 403}, {map[string]bool{"ext-test.read": true}, `{"value":"2"}`, 403}, {map[string]bool{"ext-test.write": true}, `{"value":"2"}`, 200}, {map[string]bool{"ext-test.write": true}, `{"value":"NaN"}`, 400}, {map[string]bool{"ext-test.write": true}, `{"value":"2","secret":"x"}`, 400}} {
		req := httptest.NewRequest("POST", "/api/ext-test/actions/calculate", strings.NewReader(tc.body))
		req = req.WithContext(access.WithPrincipal(req.Context(), access.Principal{UserID: "member", HouseholdID: "home", Permissions: tc.permissions}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String(), tc.status)
		}
	}
	if err := r.SetEnabled(context.Background(), "ext-test", false); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/ext-test", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("disabled plugin accessible")
	}
}

func TestBackupPinsPackageAndNeverRunsCode(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "backup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := testPackage(t, false)
	b := Backup{p}
	for _, schema := range b.Tables() {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Export(context.Background(), nil, tx, "home", true); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if _, err := b.Inspect(context.Background(), db, ""); err != nil {
		t.Fatal(err)
	}
	p.Digest = "other"
	if _, err := b.Inspect(context.Background(), db, ""); !errors.Is(err, backup.ModuleRequired) {
		t.Fatal("different executable accepted", err)
	}
}
