package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const MemoryPages = 512 // 32 MiB of guest linear memory; host/compiler memory is separate.
const ExecutionTimeout = time.Second
const maxOutput = 16 << 10

var ErrBusy = errors.New("plugin runtime busy")

// Engine admits one action globally and keeps only the most recently compiled
// plugin. Idle installations never initialize a runtime or run plugin callbacks.
type Engine struct {
	Store    *Store
	mu       sync.Mutex
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	digest   string
}

func runtimeConfig() wazero.RuntimeConfig {
	return wazero.NewRuntimeConfigCompiler().WithMemoryLimitPages(MemoryPages).WithCloseOnContextDone(true)
}
func checkModule(c wazero.CompiledModule, storage bool) error {
	start := c.ExportedFunctions()["_start"]
	if len(c.ImportedMemories()) > 0 || start == nil || len(start.ParamTypes()) != 0 || len(start.ResultTypes()) != 0 {
		return fmt.Errorf("plugin must be a WASI command")
	}
	for _, f := range c.ImportedFunctions() {
		mod, name, _ := f.Import()
		if mod != "wasi_snapshot_preview1" && !(storage && mod == "homeatlas_storage_v1" && name == "call") {
			return fmt.Errorf("unsupported host import: %s", mod)
		}
	}
	return nil
}
func validateWasm(ctx context.Context, wasm []byte) error {
	return validateGuest(ctx, wasm, false)
}
func validateGuest(ctx context.Context, wasm []byte, storage bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(MemoryPages).WithCloseOnContextDone(true))
	defer r.Close(context.Background())
	c, err := r.CompileModule(ctx, wasm)
	if err != nil {
		return err
	}
	return checkModule(c, storage)
}
func (e *Engine) Close() error { e.mu.Lock(); defer e.mu.Unlock(); return e.close() }

// Pause evicts compiled code before a large Core operation and prevents a guest
// from allocating alongside it. The caller must invoke the returned release.
func (e *Engine) Pause() func() {
	e.mu.Lock()
	if e.runtime != nil {
		e.close()
		debug.FreeOSMemory()
	}
	if e.Store != nil {
		e.Store.discardCache()
	}
	return e.mu.Unlock
}
func (e *Engine) close() error {
	var err error
	if e.runtime != nil {
		err = e.runtime.Close(context.Background())
	}
	e.runtime = nil
	e.compiled = nil
	e.digest = ""
	return err
}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	limit := b.limit
	if limit == 0 {
		limit = maxOutput
	}
	if len(p) > limit-b.Len() {
		return 0, fmt.Errorf("plugin output exceeds limit")
	}
	return b.Buffer.Write(p)
}

type Result struct {
	Text string `json:"text"`
}

func (e *Engine) Run(ctx context.Context, p *Package, view string, input map[string]string) (Result, error) {
	if !e.mu.TryLock() {
		return Result{}, ErrBusy
	}
	defer e.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var action *storageAction
	if p.Manifest.Storage != "" {
		if e.Store == nil {
			return Result{}, ErrStorageDenied
		}
		readOnly := false
		for _, v := range p.Manifest.Views {
			if v.ID == view {
				readOnly = v.ReadOnly
			}
		}
		var err error
		action, err = e.Store.begin(ctx, p, readOnly)
		if err != nil {
			return Result{}, err
		}
		ctx = context.WithValue(ctx, storageActionKey{}, action)
	}
	if e.digest != p.Digest {
		if err := e.close(); err != nil {
			return Result{}, err
		}
		e.runtime = wazero.NewRuntimeWithConfig(ctx, runtimeConfig())
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, e.runtime); err != nil {
			e.close()
			return Result{}, err
		}
		if p.Manifest.Storage != "" {
			if _, err := e.runtime.NewHostModuleBuilder("homeatlas_storage_v1").NewFunctionBuilder().WithFunc(storageCall).Export("call").Instantiate(ctx); err != nil {
				e.close()
				return Result{}, err
			}
		}
		c, err := e.runtime.CompileModule(ctx, p.wasm)
		if err != nil {
			e.close()
			return Result{}, err
		}
		if err := checkModule(c, p.Manifest.Storage != ""); err != nil {
			e.close()
			return Result{}, err
		}
		e.compiled = c
		e.digest = p.Digest
	}
	execution, stop := context.WithTimeout(ctx, ExecutionTimeout)
	defer stop()
	request, _ := json.Marshal(struct {
		View  string            `json:"view"`
		Input map[string]string `json:"input"`
	}{view, input})
	var out boundedOutput
	textLimit := 12000
	// Collection metadata may expand twice when JSON escapes HTML or separators.
	// Keep the decoded metadata bound at 16 KiB and its outer envelope at 32 KiB.
	if p.Manifest.Collection != nil {
		out.limit = 32 << 10
		textLimit = 16 << 10
	}
	// No mounts, sockets, environment, arguments or host handles are inherited.
	instance, err := e.runtime.InstantiateModule(execution, e.compiled, wazero.NewModuleConfig().WithName("").WithStdin(bytes.NewReader(request)).WithStdout(&out).WithStderr(io.Discard))
	if instance != nil {
		instance.Close(context.Background())
	}
	if err != nil {
		if action != nil && action.failed != nil {
			return Result{}, action.failed
		}
		return Result{}, err
	}
	var result Result
	if err := decode(out.Bytes(), &result); err != nil || !text(result.Text, textLimit) {
		return Result{}, fmt.Errorf("invalid plugin output")
	}
	if action != nil {
		if err := action.commit(execution); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

type Module struct {
	Package *Package
	Engine  *Engine
}

func (m *Module) Manifest() modules.Manifest {
	p := m.Package.Manifest
	return modules.Manifest{ID: p.ID, Name: p.Name, Version: p.Version, ContractVersion: modules.ContractVersion, Runtime: "wasi-preview1", Capabilities: m.Package.Capabilities(), Dependencies: []string{}, Navigation: modules.Navigation{Label: p.Name, Path: "/" + p.ID}}
}
func (*Module) Migrations() []database.Migration { return nil }
func (m *Module) SearchPermission() string       { return m.Package.Manifest.ID + ".read" }
func (*Module) Search(context.Context, string) ([]modules.SearchResult, error) {
	return []modules.SearchResult{}, nil
}
func (m *Module) Routes() []modules.Route {
	routes := []modules.Route{
		{Method: "GET", Path: "/", Permission: m.SearchPermission(), Handler: func(w http.ResponseWriter, r *http.Request) { modules.JSON(w, 200, m.Package.Manifest) }},
	}
	for _, view := range m.Package.Manifest.Views {
		permission := m.Package.Manifest.ID + ".write"
		if view.ReadOnly {
			permission = m.SearchPermission()
		}
		if view.Permission == "share" {
			permission = m.Package.Manifest.ID + ".share"
		}
		routes = append(routes, modules.Route{Method: "POST", Path: "/actions/" + view.ID, Permission: permission, Handler: func(w http.ResponseWriter, r *http.Request) { m.run(w, r, view) }})
	}
	return routes
}
func (m *Module) run(w http.ResponseWriter, r *http.Request, view View) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	var input map[string]string
	if err == nil {
		err = decode(body, &input)
	}
	valid := err == nil && len(input) <= len(view.Fields)
	known := map[string]bool{}
	for _, f := range view.Fields {
		known[f.Name] = true
		value := input[f.Name]
		if len(value) > 2000 || (f.Required && !text(value, 2000)) {
			valid = false
		}
		if f.Type == "number" && value != "" {
			if n, err := strconv.ParseFloat(value, 64); err != nil || n != n || n > 1e15 || n < -1e15 {
				valid = false
			}
		}
	}
	for name := range input {
		if !known[name] {
			valid = false
		}
	}
	if !valid {
		modules.JSON(w, 400, map[string]string{"error": "invalid_plugin_input"})
		return
	}
	result, err := m.Engine.Run(r.Context(), m.Package, view.ID, input)
	if errors.Is(err, ErrStorageDenied) {
		modules.JSON(w, 403, map[string]string{"error": "permission_denied"})
		return
	}
	if errors.Is(err, ErrStorageConflict) {
		modules.JSON(w, 409, map[string]string{"error": "plugin_storage_changed"})
		return
	}
	if errors.Is(err, ErrBusy) {
		w.Header().Set("Retry-After", "1")
		modules.JSON(w, 429, map[string]string{"error": "plugin_busy"})
		return
	}
	if err != nil {
		modules.JSON(w, 502, map[string]string{"error": "plugin_failed"})
		return
	}
	modules.JSON(w, 200, result)
}
