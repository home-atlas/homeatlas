// Package plugins loads locally approved WASI extensions with bounded host APIs.
// Guests never receive database handles, host files, environment or network.
package packages

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	modules "github.com/home-atlas/homeatlas/internal/core/modules/domain"
	"github.com/home-atlas/homeatlas/internal/core/plugins/adapters/wasi"
	"github.com/home-atlas/homeatlas/internal/core/plugins/domain"
)

type receipt struct {
	SHA256      string   `json:"sha256"`
	Permissions []string `json:"permissions"`
}

// Read validates bounded contents without extracting paths or running guest code.
func Read(data []byte) (*Package, error) {
	if len(data) > MaxPackageBytes {
		return nil, fmt.Errorf("package exceeds 10 MiB")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(z.File) != 2 {
		return nil, fmt.Errorf("package must contain only manifest.json and plugin.wasm")
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		limit := int64(maxWasmBytes)
		if f.Name == "manifest.json" {
			limit = 32 << 10
		} else if f.Name != "plugin.wasm" {
			return nil, fmt.Errorf("unexpected package path %q", f.Name)
		}
		if _, exists := files[f.Name]; exists || !f.Mode().IsRegular() || f.UncompressedSize64 > uint64(limit) {
			return nil, fmt.Errorf("invalid package entry %q", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(r, limit+1))
		closeErr := r.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(len(body)) > limit {
			return nil, fmt.Errorf("entry exceeds limit")
		}
		files[f.Name] = body
	}
	p := &Package{WASM: files["plugin.wasm"], Digest: fmt.Sprintf("%x", sha256.Sum256(data))}
	if err := domain.Decode(files["manifest.json"], &p.Manifest); err != nil {
		return nil, err
	}
	if err := domain.ValidatePackage(p); err != nil {
		return nil, err
	}
	return p, nil
}

func readFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid or oversized file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file too large")
	}
	return b, err
}

// Install is an offline administrative operation. A digest pins the reviewed
// artifact; an exact permission list is explicit approval, not a signature.
func Install(ctx context.Context, dir, archive, digest string, approved []string) (*Package, error) {
	if b, err := hex.DecodeString(digest); err != nil || len(b) != 32 {
		return nil, fmt.Errorf("a package SHA-256 is required")
	}
	data, err := readFile(archive, MaxPackageBytes)
	if err != nil {
		return nil, err
	}
	p, err := Read(data)
	if err != nil {
		return nil, err
	}
	if p.Digest != strings.ToLower(digest) {
		return nil, fmt.Errorf("package SHA-256 mismatch")
	}
	slices.Sort(approved)
	want := p.Permissions()
	slices.Sort(want)
	if !slices.Equal(approved, want) {
		return nil, fmt.Errorf("explicit approval required for %s", strings.Join(want, ","))
	}
	if err := wasi.ValidateGuest(ctx, p.WASM, p.Manifest.Storage != ""); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	count := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			count++
		}
	}
	if count >= maxPlugins {
		return nil, fmt.Errorf("at most %d external plugins", maxPlugins)
	}
	target := filepath.Join(dir, p.Manifest.ID)
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return nil, fmt.Errorf("plugin already installed or inaccessible")
	}
	stage, err := os.MkdirTemp(dir, ".install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	raw, _ := json.Marshal(receipt{p.Digest, want})
	for name, body := range map[string][]byte{"package.zip": data, "approval.json": raw} {
		f, err := os.OpenFile(filepath.Join(stage, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, err = f.Write(body)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if err := syncDir(stage); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, target); err != nil {
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		return nil, err
	}
	return p, nil
}
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func Load(dir string) ([]*Package, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []*Package
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() || !modules.ValidID(e.Name()) || len(result) >= maxPlugins {
			return nil, fmt.Errorf("invalid installed plugin directory")
		}
		base := filepath.Join(dir, e.Name())
		for _, name := range []string{"package.zip", "approval.json"} {
			info, err := os.Lstat(filepath.Join(base, name))
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("invalid installed plugin file")
			}
		}
		data, err := readFile(filepath.Join(base, "package.zip"), MaxPackageBytes)
		if err != nil {
			return nil, err
		}
		p, err := Read(data)
		if err != nil {
			return nil, err
		}
		raw, err := readFile(filepath.Join(base, "approval.json"), 4096)
		if err != nil {
			return nil, err
		}
		var r receipt
		if err := domain.Decode(raw, &r); err != nil {
			return nil, err
		}
		want := p.Permissions()
		slices.Sort(want)
		slices.Sort(r.Permissions)
		if p.Manifest.ID != e.Name() || p.Digest != r.SHA256 || !slices.Equal(want, r.Permissions) {
			return nil, fmt.Errorf("installed plugin approval/integrity mismatch")
		}
		result = append(result, p)
	}
	return result, nil
}

type Package = domain.Package

const (
	MaxPackageBytes = domain.MaxPackageBytes
	maxWasmBytes    = domain.MaxWasmBytes
	maxPlugins      = domain.MaxPlugins
)
