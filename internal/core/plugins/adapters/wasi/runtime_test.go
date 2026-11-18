package wasi

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/plugins/application"
	"github.com/home-atlas/homeatlas/internal/core/plugins/domain"
)

type Package = domain.Package
type testEngine struct {
	*Engine
	runner *application.Runner
}

func (e *testEngine) Run(ctx context.Context, p *Package, view string, input map[string]string) (domain.Result, error) {
	return e.runner.Run(ctx, nil, p, view, input)
}
func runtimePackage(t *testing.T, loop bool) *Package {
	t.Helper()
	wasm := command(`{"text":"ok"}`, loop)
	return &Package{Manifest: domain.Manifest{ID: "ext-test", Views: []domain.View{{ID: "calculate"}}}, Digest: fmt.Sprintf("%x", sha256.Sum256(wasm)), WASM: wasm}
}

var ErrBusy = domain.ErrBusy

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
func TestRuntimeDeadlineBoundedOutputAdmissionAndWarmReuse(t *testing.T) {
	native := &Engine{}
	e := &testEngine{Engine: native, runner: application.NewRunner(native)}
	defer e.Close()
	p := runtimePackage(t, false)
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
	if _, err := e.Run(ctx, runtimePackage(t, true), "calculate", nil); err == nil || time.Since(started) > time.Second {
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
	bad := runtimePackage(t, false)
	bad.WASM = command(`{"text":"ok","html":"bad"}`, false)
	bad.Digest = "other"
	if _, err := e.Run(context.Background(), bad, "calculate", nil); err == nil {
		t.Fatal("unsupported guest output accepted")
	}
}
