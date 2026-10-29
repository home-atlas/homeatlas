//go:build wasip1

// Disposable resource/integrity fixture, not a product module.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/home-atlas/homeatlas/sdk/pluginstorage"
)

func main() {
	var r struct {
		View  string            `json:"view"`
		Input map[string]string `json:"input"`
	}
	if json.NewDecoder(os.Stdin).Decode(&r) != nil {
		os.Exit(1)
	}
	start, e1 := strconv.Atoi(r.Input["start"])
	count, e2 := strconv.Atoi(r.Input["count"])
	size, e3 := strconv.Atoi(r.Input["bytes"])
	seed, e4 := strconv.Atoi(r.Input["seed"])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || start < 0 || count < 1 || count > 32 || size < 1 || size > 16384 || seed < 1 {
		os.Exit(1)
	}
	for i := start; i < start+count; i++ {
		key := fmt.Sprintf("record-%08d", i)
		x := uint64(i+1) ^ (uint64(seed) << 32)
		value := make([]byte, size)
		for j := range value {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			value[j] = byte('!' + x%90)
		}
		switch r.View {
		case "shared_write":
			if pluginstorage.SetShared(key, string(value)) != nil {
				os.Exit(5)
			}
		case "shared_check":
			v, found, err := pluginstorage.GetShared(key)
			if err != nil || !found || v != string(value) {
				os.Exit(6)
			}
		case "shared_missing":
			_, found, err := pluginstorage.GetShared(key)
			if err != nil || found {
				os.Exit(7)
			}
		case "grant":
			permission := r.Input["permission"]
			if permission == "" {
				permission = "write"
			}
			if pluginstorage.GrantShared(key, r.Input["user"], permission) != nil {
				os.Exit(8)
			}
		case "revoke":
			if pluginstorage.RevokeShared(key, r.Input["user"]) != nil {
				os.Exit(9)
			}
		case "write":
			if pluginstorage.Set(key, string(value)) != nil {
				os.Exit(2)
			}
		case "check":
			v, found, err := pluginstorage.Get(key)
			if err != nil || !found || v != string(value) {
				os.Exit(3)
			}
		case "delete":
			if pluginstorage.Delete(key) != nil {
				os.Exit(4)
			}
		default:
			os.Exit(1)
		}
	}
	if json.NewEncoder(os.Stdout).Encode(struct {
		Text string `json:"text"`
	}{fmt.Sprintf("%s %d", r.View, count)}) != nil {
		os.Exit(1)
	}
}
