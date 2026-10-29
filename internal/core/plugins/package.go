// Package plugins loads locally approved WASI extensions with bounded host APIs.
// Guests never receive database handles, host files, environment or network.
package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/home-atlas/homeatlas/internal/core/collections"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const MaxPackageBytes = 10 << 20
const maxWasmBytes = 8 << 20
const maxPlugins = 8

var pluginID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var key = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

type Field struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}
type View struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	SubmitLabel string  `json:"submitLabel"`
	Fields      []Field `json:"fields"`
	ReadOnly    bool    `json:"readOnly,omitempty"`
	Permission  string  `json:"permission,omitempty"`
}
type Manifest struct {
	Collection      *collections.Definition `json:"collection,omitempty"`
	ContractVersion string                  `json:"contractVersion"`
	ID              string                  `json:"id"`
	Name            string                  `json:"name"`
	Version         string                  `json:"version"`
	WasmSHA256      string                  `json:"wasmSHA256"`
	Storage         string                  `json:"storage,omitempty"`
	Views           []View                  `json:"views"`
}
type Package struct {
	Manifest Manifest
	Digest   string
	wasm     []byte
}
type receipt struct {
	SHA256      string   `json:"sha256"`
	Permissions []string `json:"permissions"`
}

func (p *Package) Permissions() []string {
	permissions := p.Capabilities()
	if p.Manifest.Collection != nil {
		permissions = append(permissions, p.Manifest.ID+".files", p.Manifest.ID+".collection")
	}
	if p.Manifest.Storage != "" {
		permissions = append(permissions, p.Manifest.ID+".storage")
	}
	if p.Manifest.Storage == "shared" {
		permissions = append(permissions, p.Manifest.ID+".storage.shared")
	}
	return permissions
}

// Storage is an installation consent for the host API, not an extra account
// grant. Existing read/write role policies continue to govern each user's data.
func (p *Package) Capabilities() []string {
	caps := []string{p.Manifest.ID + ".read", p.Manifest.ID + ".write"}
	if p.Manifest.Storage == "shared" || p.Manifest.Collection != nil {
		caps = append(caps, p.Manifest.ID+".share")
	}
	return caps
}
func text(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && !strings.ContainsAny(s, "\x00\r")
}

func decode(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
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
	p := &Package{wasm: files["plugin.wasm"], Digest: fmt.Sprintf("%x", sha256.Sum256(data))}
	if err := decode(files["manifest.json"], &p.Manifest); err != nil {
		return nil, err
	}
	m := p.Manifest
	if m.ContractVersion != "1" || (m.Storage != "" && m.Storage != "user" && m.Storage != "shared") || !pluginID.MatchString(m.ID) || !text(m.Name, 100) || !text(m.Version, 40) || len(m.Views) < 1 || len(m.Views) > 8 || m.WasmSHA256 != fmt.Sprintf("%x", sha256.Sum256(p.wasm)) {
		return nil, fmt.Errorf("invalid or incompatible plugin manifest")
	}
	if m.Collection != nil {
		if m.Collection.LegacySnapshot && m.ID != "documents" {
			return nil, fmt.Errorf("unsupported legacy collection")
		}
		if m.Storage != "" {
			return nil, fmt.Errorf("collections cannot import record storage")
		}
		config := collections.Config{ID: m.ID, Name: m.Name, Version: m.Version, Definition: *m.Collection, Validate: func(_ context.Context, b []byte) ([]byte, error) { return b, nil }}
		if err := config.Valid(); err != nil {
			return nil, err
		}
	}
	reserved := map[string]bool{"core": true, "identity": true, "schema": true, "module": true, "plugin": true, "auth": true, "setup": true, "household": true, "health": true, "search": true, "modules": true, "settings": true, "family": true, "login": true, "join": true, "recover": true, "invitations": true, "backups": true, "plugins": true, "plugin-files": true}
	if reserved[m.ID] {
		return nil, fmt.Errorf("reserved plugin ID")
	}
	if m.Collection == nil && !strings.HasPrefix(m.ID, "ext-") {
		return nil, fmt.Errorf("non-collection plugins need ext- IDs")
	}
	seen := map[string]bool{}
	for _, v := range m.Views {
		if v.Permission != "" && (v.Permission != "share" || m.Storage != "shared") {
			return nil, fmt.Errorf("invalid view permission")
		}
		if !key.MatchString(v.ID) || seen[v.ID] || !text(v.Title, 120) || len(v.Description) > 1000 || !text(v.SubmitLabel, 80) || len(v.Fields) > 12 {
			return nil, fmt.Errorf("invalid view")
		}
		seen[v.ID] = true
		fields := map[string]bool{}
		for _, f := range v.Fields {
			if !key.MatchString(f.Name) || fields[f.Name] || !text(f.Label, 120) || (f.Type != "text" && f.Type != "number" && f.Type != "textarea") {
				return nil, fmt.Errorf("invalid field")
			}
			fields[f.Name] = true
		}
	}
	if m.Collection != nil && !seen["validate"] {
		return nil, fmt.Errorf("collection validation view required")
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
	if err := validateGuest(ctx, p.wasm, p.Manifest.Storage != ""); err != nil {
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
		if !e.IsDir() || !pluginID.MatchString(e.Name()) || len(result) >= maxPlugins {
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
		if err := decode(raw, &r); err != nil {
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
