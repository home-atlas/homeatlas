package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	collections "github.com/home-atlas/homeatlas/internal/core/collections/domain"
	modules "github.com/home-atlas/homeatlas/internal/core/modules/domain"
)

const (
	MaxPackageBytes   = 10 << 20
	MaxWasmBytes      = 8 << 20
	MaxPlugins        = 8
	StorageValueBytes = 16 << 10
	StorageRPCBytes   = 128 << 10
	StorageCalls      = 64
	StoragePageKeys   = 64
	MemoryPages       = 512
	ExecutionTimeout  = time.Second
	MaxOutput         = 16 << 10
)

var (
	key                = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	StorageKey         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	StorageIdentity    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	ErrStorageDenied   = errors.New("plugin storage permission denied")
	ErrStorageConflict = errors.New("plugin storage changed")
	ErrStorageInvalid  = errors.New("invalid plugin storage operation")
	ErrBusy            = errors.New("plugin runtime busy")
)

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
	WASM     []byte `json:"-"`
}
type StorageRequest struct {
	Op         string  `json:"op"`
	Key        string  `json:"key,omitempty"`
	Value      *string `json:"value,omitempty"`
	After      string  `json:"after,omitempty"`
	Limit      int     `json:"limit,omitempty"`
	User       string  `json:"user,omitempty"`
	Permission string  `json:"permission,omitempty"`
}
type StorageResponse struct {
	Found  bool          `json:"found,omitempty"`
	Value  *string       `json:"value,omitempty"`
	Keys   []string      `json:"keys,omitempty"`
	Next   string        `json:"next,omitempty"`
	OK     bool          `json:"ok,omitempty"`
	Error  string        `json:"error,omitempty"`
	Grants []SharedGrant `json:"grants,omitempty"`
}
type SharedRecord struct{ Key, ID, Owner, Member, Value string }
type SharedChange struct{ Before, After *SharedRecord }
type GrantKey struct{ Record, User string }
type SharedGrant struct {
	User       string `json:"user"`
	Permission string `json:"permission"`
	Member     string `json:"-"`
	Record     string `json:"-"`
	Key        string `json:"-"`
}
type Result struct {
	Text string `json:"text"`
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
func (p *Package) Capabilities() []string {
	caps := []string{p.Manifest.ID + ".read", p.Manifest.ID + ".write"}
	if p.Manifest.Storage == "shared" || p.Manifest.Collection != nil {
		caps = append(caps, p.Manifest.ID+".share")
	}
	return caps
}
func Text(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && !strings.ContainsAny(s, "\x00\r")
}
func Decode(data []byte, target any) error {
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
func ValidatePackage(p *Package) error {
	m := p.Manifest
	if m.ContractVersion != "1" || (m.Storage != "" && m.Storage != "user" && m.Storage != "shared") || !modules.ValidID(m.ID) || !Text(m.Name, 100) || !Text(m.Version, 40) || len(m.Views) < 1 || len(m.Views) > 8 || m.WasmSHA256 != fmt.Sprintf("%x", sha256.Sum256(p.WASM)) {
		return fmt.Errorf("invalid or incompatible plugin manifest")
	}
	if m.Collection != nil {
		if m.Collection.LegacySnapshot && m.ID != "documents" {
			return fmt.Errorf("unsupported legacy collection")
		}
		if m.Storage != "" {
			return fmt.Errorf("collections cannot import record storage")
		}

		if err := collections.ValidateDefinition(m.ID, m.Name, m.Version, *m.Collection); err != nil {
			return err
		}
	}
	if m.Collection == nil && !strings.HasPrefix(m.ID, "ext-") {
		return fmt.Errorf("non-collection plugins need ext- IDs")
	}
	seen := map[string]bool{}
	for _, v := range m.Views {
		if v.Permission != "" && (v.Permission != "share" || m.Storage != "shared") {
			return fmt.Errorf("invalid view permission")
		}
		if !key.MatchString(v.ID) || seen[v.ID] || !Text(v.Title, 120) || len(v.Description) > 1000 || !Text(v.SubmitLabel, 80) || len(v.Fields) > 12 {
			return fmt.Errorf("invalid view")
		}
		seen[v.ID] = true
		fields := map[string]bool{}
		for _, f := range v.Fields {
			if !key.MatchString(f.Name) || fields[f.Name] || !Text(f.Label, 120) || (f.Type != "text" && f.Type != "number" && f.Type != "textarea") {
				return fmt.Errorf("invalid field")
			}
			fields[f.Name] = true
		}
	}
	if m.Collection != nil && !seen["validate"] {
		return fmt.Errorf("collection validation view required")
	}
	return nil
}
