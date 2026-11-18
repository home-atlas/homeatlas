package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStorageABIWireContract(t *testing.T) {
	value := "hello"
	raw, err := json.Marshal(StorageRequest{Op: "set", Key: "note", Value: &value})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"op":"set","key":"note","value":"hello"}` {
		t.Fatal("SDK request ABI changed", string(raw))
	}
	raw, err = json.Marshal(StorageResponse{OK: true, Grants: []SharedGrant{{User: "reader", Permission: "read", Member: "private-membership", Record: "private-record", Key: "private-key"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-") {
		t.Fatal("private grant metadata became public", string(raw))
	}
	raw, err = json.Marshal(Package{WASM: []byte("private-bytecode")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "WASM") {
		t.Fatal("package bytecode became public", string(raw))
	}
}
