package plugins

import (
	"context"

	"github.com/home-atlas/homeatlas/internal/core/plugins/adapters/wasi"
	"github.com/home-atlas/homeatlas/internal/core/plugins/application"
	"github.com/home-atlas/homeatlas/internal/core/plugins/domain"
)

type storageAction = application.Action
type storageRequest = domain.StorageRequest
type storageResponse = domain.StorageResponse

const maxWasmBytes = domain.MaxWasmBytes

var decode = domain.Decode
var validateGuest = wasi.ValidateGuest

func validateWasm(ctx context.Context, wasm []byte) error {
	return wasi.ValidateGuest(ctx, wasm, false)
}
