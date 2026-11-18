package wasi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"sync"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/plugins/application"
	"github.com/home-atlas/homeatlas/internal/core/plugins/domain"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type Engine struct {
	Discard  func()
	mu       sync.Mutex
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	digest   string
}

type boundedOutput struct {
	bytes.Buffer
	limit int
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
	return ValidateGuest(ctx, wasm, false)
}
func ValidateGuest(ctx context.Context, wasm []byte, storage bool) error {
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
func (e *Engine) Pause() func() {
	e.mu.Lock()
	if e.runtime != nil {
		e.close()
		debug.FreeOSMemory()
	}
	if e.Discard != nil {
		e.Discard()
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
func (e *Engine) Acquire() (func(), error) {
	if !e.mu.TryLock() {
		return nil, domain.ErrBusy
	}
	return e.mu.Unlock, nil
}
func (e *Engine) Start(ctx context.Context, p *domain.Package, request []byte, limit int) (application.Execution, error) {
	if e.digest != p.Digest {
		if err := e.close(); err != nil {
			return nil, err
		}
		e.runtime = wazero.NewRuntimeWithConfig(ctx, runtimeConfig())
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, e.runtime); err != nil {
			e.close()
			return nil, err
		}
		if p.Manifest.Storage != "" {
			if _, err := e.runtime.NewHostModuleBuilder("homeatlas_storage_v1").NewFunctionBuilder().WithFunc(storageCall).Export("call").Instantiate(ctx); err != nil {
				e.close()
				return nil, err
			}
		}
		c, err := e.runtime.CompileModule(ctx, p.WASM)
		if err != nil {
			e.close()
			return nil, err
		}
		if err := checkModule(c, p.Manifest.Storage != ""); err != nil {
			e.close()
			return nil, err
		}
		e.compiled = c
		e.digest = p.Digest
	}
	executionContext, stop := context.WithTimeout(ctx, ExecutionTimeout)
	var out boundedOutput
	out.limit = limit
	// Collection metadata may expand twice when JSON escapes HTML or separators.
	// Keep the decoded metadata bound at 16 KiB and its outer envelope at 32 KiB.
	if p.Manifest.Collection != nil {
		out.limit = 32 << 10
	}
	// No mounts, sockets, environment, arguments or host handles are inherited.
	instance, err := e.runtime.InstantiateModule(executionContext, e.compiled, wazero.NewModuleConfig().WithName("").WithStdin(bytes.NewReader(request)).WithStdout(&out).WithStderr(io.Discard))
	if instance != nil {
		instance.Close(context.Background())
	}
	if err != nil {
		stop()
		return nil, err
	}
	return &execution{ctx: executionContext, stop: stop, output: out.Bytes()}, nil

}

type execution struct {
	ctx    context.Context
	stop   context.CancelFunc
	output []byte
}

func (e *execution) Context() context.Context { return e.ctx }
func (e *execution) Output() []byte           { return e.output }
func (e *execution) Close()                   { e.stop() }
func storageCall(ctx context.Context, m api.Module, ptr, length, out, capacity uint32) uint32 {
	a, ok := application.ActionFrom(ctx)
	if !ok || m.Memory() == nil {
		return 0
	}
	if length > StorageRPCBytes || capacity < StorageRPCBytes {
		a.Fail(domain.ErrStorageInvalid)
		return 0
	}
	raw, ok := m.Memory().Read(ptr, length)
	if !ok {
		a.Fail(domain.ErrStorageInvalid)
		return 0
	}
	if _, ok := m.Memory().Read(out, StorageRPCBytes); !ok {
		a.Fail(domain.ErrStorageInvalid)
		return 0
	}
	var req domain.StorageRequest
	err := domain.Decode(raw, &req)
	var result domain.StorageResponse
	if err == nil {
		result, err = a.Call(ctx, req)
	}
	if err != nil {
		a.Fail(err)
		result = domain.StorageResponse{Error: "storage_failed"}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > StorageRPCBytes || !m.Memory().Write(out, encoded) {
		a.Fail(domain.ErrStorageInvalid)
		return 0
	}
	return uint32(len(encoded))
}

const (
	MemoryPages       = domain.MemoryPages
	ExecutionTimeout  = domain.ExecutionTimeout
	maxOutput         = domain.MaxOutput
	StorageValueBytes = domain.StorageValueBytes
	StorageRPCBytes   = domain.StorageRPCBytes
)
