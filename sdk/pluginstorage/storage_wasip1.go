//go:build wasip1

// Package pluginstorage is the experimental private/shared storage ABI for WASI
// plugins. The host supplies identity and authorization; callers cannot set them.
package pluginstorage

import (
	"encoding/json"
	"errors"
	"runtime"
	"unsafe"
)

const rpcBytes = 128 << 10

//go:wasmimport homeatlas_storage_v1 call
//go:noescape
func hostCall(request, length, response, capacity uint32) uint32

type request struct {
	Op         string  `json:"op"`
	Key        string  `json:"key,omitempty"`
	Value      *string `json:"value,omitempty"`
	After      string  `json:"after,omitempty"`
	Limit      int     `json:"limit,omitempty"`
	User       string  `json:"user,omitempty"`
	Permission string  `json:"permission,omitempty"`
}
type response struct {
	Found  bool     `json:"found"`
	Value  string   `json:"value"`
	Keys   []string `json:"keys"`
	Next   string   `json:"next"`
	Error  string   `json:"error"`
	Grants []Grant  `json:"grants"`
}

type Grant struct {
	User       string `json:"user"`
	Permission string `json:"permission"`
}

func call(req request) (response, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return response{}, err
	}
	if len(raw) > rpcBytes {
		return response{}, errors.New("storage request too large")
	}
	buffer := make([]byte, rpcBytes)
	n := hostCall(uint32(uintptr(unsafe.Pointer(&raw[0]))), uint32(len(raw)), uint32(uintptr(unsafe.Pointer(&buffer[0]))), rpcBytes)
	runtime.KeepAlive(raw)
	if n == 0 || n > rpcBytes {
		return response{}, errors.New("storage host call failed")
	}
	var result response
	if err := json.Unmarshal(buffer[:n], &result); err != nil {
		return response{}, err
	}
	if result.Error != "" {
		return response{}, errors.New(result.Error)
	}
	return result, nil
}
func Get(key string) (value string, found bool, err error) {
	r, err := call(request{Op: "get", Key: key})
	return r.Value, r.Found, err
}
func Set(key, value string) error {
	_, err := call(request{Op: "set", Key: key, Value: &value})
	return err
}
func Delete(key string) error { _, err := call(request{Op: "delete", Key: key}); return err }

// List returns at most 64 keys and an opaque continuation key, or an empty next
// value at the end. Pass next as after in a later action to continue.
func List(after string, limit int) (keys []string, next string, err error) {
	r, err := call(request{Op: "list", After: after, Limit: limit})
	return r.Keys, r.Next, err
}

func GetShared(key string) (value string, found bool, err error) {
	r, err := call(request{Op: "shared.get", Key: key})
	return r.Value, r.Found, err
}
func SetShared(key, value string) error {
	_, err := call(request{Op: "shared.set", Key: key, Value: &value})
	return err
}

// Only the creator can delete or manage grants. Editors can update values only.
func DeleteShared(key string) error {
	_, err := call(request{Op: "shared.delete", Key: key})
	return err
}
func ListShared(after string, limit int) (keys []string, next string, err error) {
	r, err := call(request{Op: "shared.list", After: after, Limit: limit})
	return r.Keys, r.Next, err
}
func GrantShared(key, user, permission string) error {
	_, err := call(request{Op: "shared.grant", Key: key, User: user, Permission: permission})
	return err
}
func RevokeShared(key, user string) error {
	_, err := call(request{Op: "shared.revoke", Key: key, User: user})
	return err
}
func ListSharedGrants(key, after string, limit int) (grants []Grant, next string, err error) {
	r, err := call(request{Op: "shared.grants", Key: key, After: after, Limit: limit})
	return r.Grants, r.Next, err
}
