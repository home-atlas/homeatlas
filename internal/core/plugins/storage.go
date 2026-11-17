package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
	"github.com/tetratelabs/wazero/api"
)

const StorageValueBytes = 16 << 10
const StorageRPCBytes = 128 << 10 // Includes worst-case JSON escaping of a value.
const StorageCalls = 64
const StoragePageKeys = 64

var storageKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var storageIdentity = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var ErrStorageDenied = errors.New("plugin storage permission denied")
var ErrStorageConflict = errors.New("plugin storage changed")
var errStorageInvalid = errors.New("invalid plugin storage operation")

type Store struct {
	repository *storageRepository
	path       string
}

// A membership is part of the namespace: removing and reinviting an account must
// not make its historical private records accessible to the new membership.
// Restores may leave a large, reusable WAL plus the main database in cache.
// SQLite owns durability; advice only discards clean pages and never syncs,
// truncates or changes journal settings. Unsupported filesystems may ignore it.
func (s *Store) discardCache() {
	if s.path == "" {
		return
	}
	for _, path := range []string{s.path, s.path + "-wal"} {
		if file, err := os.Open(path); err == nil {
			backup.DiscardCache(file)
			file.Close()
		}
	}
}

type storageAction struct {
	store            *Store
	plugin           string
	principal        access.Principal
	revision         int64
	readOnly         bool
	pending          map[string]*string
	calls            int
	failed           error
	shared           bool
	sharedRevision   int64
	sharedTouched    bool
	sharedNeedsShare bool
	sharedPending    map[string]sharedChange
	grantsPending    map[grantKey]*sharedGrant
}
type storageActionKey struct{}

func (s *Store) begin(ctx context.Context, p *Package, readOnly bool) (*storageAction, error) {
	principal, ok := access.Current(ctx)
	if !ok || !storageIdentity.MatchString(principal.MembershipID) {
		return nil, ErrStorageDenied
	}
	a := &storageAction{store: s, plugin: p.Manifest.ID, principal: principal, readOnly: readOnly, pending: map[string]*string{}}
	if err := a.authorize(ctx, nil, false); err != nil {
		return nil, err
	}
	revision, err := a.currentRevision(ctx, nil)
	a.revision = revision
	if err == nil && p.Manifest.Storage == "shared" {
		a.shared = true
		a.sharedPending = map[string]sharedChange{}
		a.grantsPending = map[grantKey]*sharedGrant{}
		a.sharedRevision, err = a.currentSharedRevision(ctx, nil)
	}
	return a, err
}

// Recheck live permissions and activation at storage boundaries and commit.
// Nothing supplied by the guest participates in these namespace parameters.
func (a *storageAction) authorize(ctx context.Context, tx unitofwork.Transaction, write bool) error {
	if write && a.readOnly {
		return ErrStorageDenied
	}
	allowed, err := a.store.repository.allowed(ctx, tx, a.plugin, a.principal, write)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrStorageDenied
	}
	return nil
}
func (a *storageAction) currentRevision(ctx context.Context, tx unitofwork.Transaction) (int64, error) {
	revision, owner, err := a.store.repository.revision(ctx, tx, a.plugin, a.principal)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return 0, nil
	}
	if err == nil && owner != a.principal.UserID {
		return 0, ErrStorageDenied
	}
	return revision, err
}
func (a *storageAction) unchanged(ctx context.Context, tx unitofwork.Transaction) error {
	revision, err := a.currentRevision(ctx, tx)
	if err != nil {
		return err
	}
	if revision != a.revision {
		return ErrStorageConflict
	}
	return nil
}

type storageRequest struct {
	Op         string  `json:"op"`
	Key        string  `json:"key,omitempty"`
	Value      *string `json:"value,omitempty"`
	After      string  `json:"after,omitempty"`
	Limit      int     `json:"limit,omitempty"`
	User       string  `json:"user,omitempty"`
	Permission string  `json:"permission,omitempty"`
}
type storageResponse struct {
	Found  bool          `json:"found,omitempty"`
	Value  *string       `json:"value,omitempty"`
	Keys   []string      `json:"keys,omitempty"`
	Next   string        `json:"next,omitempty"`
	OK     bool          `json:"ok,omitempty"`
	Error  string        `json:"error,omitempty"`
	Grants []sharedGrant `json:"grants,omitempty"`
}

func (a *storageAction) call(ctx context.Context, req storageRequest) (storageResponse, error) {
	a.calls++
	if a.failed != nil {
		return storageResponse{}, a.failed
	}
	if a.calls > StorageCalls {
		return storageResponse{}, errStorageInvalid
	}
	if strings.HasPrefix(req.Op, "shared.") {
		return a.sharedCall(ctx, req)
	}
	if req.User != "" || req.Permission != "" {
		return storageResponse{}, errStorageInvalid
	}
	write := req.Op == "set" || req.Op == "delete"
	if err := a.authorize(ctx, nil, write); err != nil {
		return storageResponse{}, err
	}
	if err := a.unchanged(ctx, nil); err != nil {
		return storageResponse{}, err
	}
	if req.Op == "list" {
		if req.Key != "" || req.Value != nil || (req.After != "" && !storageKey.MatchString(req.After)) || req.Limit < 0 || req.Limit > StoragePageKeys {
			return storageResponse{}, errStorageInvalid
		}
		limit := req.Limit
		if limit == 0 {
			limit = StoragePageKeys
		}
		keys, err := a.store.repository.keys(ctx, a.plugin, a.principal, req.After, limit+len(a.pending)+1)
		if err != nil {
			return storageResponse{}, err
		}
		for key, value := range a.pending {
			if key > req.After {
				if value == nil {
					delete(keys, key)
				} else {
					keys[key] = true
				}
			}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		slices.Sort(ordered)
		result := storageResponse{Keys: ordered, OK: true}
		if len(ordered) > limit {
			result.Keys = ordered[:limit]
			result.Next = result.Keys[limit-1]
		}
		return result, nil
	}
	if !storageKey.MatchString(req.Key) || req.After != "" || req.Limit != 0 {
		return storageResponse{}, errStorageInvalid
	}
	switch req.Op {
	case "get":
		if req.Value != nil {
			return storageResponse{}, errStorageInvalid
		}
		if value, exists := a.pending[req.Key]; exists {
			return storageResponse{Found: value != nil, Value: value, OK: true}, nil
		}
		value, err := a.store.repository.value(ctx, a.plugin, a.principal, req.Key)
		if errors.Is(err, unitofwork.ErrNotFound) {
			return storageResponse{OK: true}, nil
		}
		if err != nil {
			return storageResponse{}, err
		}
		return storageResponse{Found: true, Value: &value, OK: true}, nil
	case "set":
		if req.Value == nil || len(*req.Value) > StorageValueBytes || !utf8.ValidString(*req.Value) {
			return storageResponse{}, errStorageInvalid
		}
		a.pending[req.Key] = req.Value
		return storageResponse{OK: true}, nil
	case "delete":
		if req.Value != nil {
			return storageResponse{}, errStorageInvalid
		}
		a.pending[req.Key] = nil
		return storageResponse{OK: true}, nil
	default:
		return storageResponse{}, errStorageInvalid
	}
}

// call is the only host function. Failures poison the action even if guest code
// ignores them. The output buffer is validated before staging any mutation.
func storageCall(ctx context.Context, m api.Module, ptr, length, out, capacity uint32) uint32 {
	a, ok := ctx.Value(storageActionKey{}).(*storageAction)
	if !ok || m.Memory() == nil {
		return 0
	}
	if length > StorageRPCBytes || capacity < StorageRPCBytes {
		a.failed = errStorageInvalid
		return 0
	}
	raw, ok := m.Memory().Read(ptr, length)
	if !ok {
		a.failed = errStorageInvalid
		return 0
	}
	if _, ok := m.Memory().Read(out, StorageRPCBytes); !ok {
		a.failed = errStorageInvalid
		return 0
	}
	var req storageRequest
	err := decode(raw, &req)
	var result storageResponse
	if err == nil {
		result, err = a.call(ctx, req)
	}
	if err != nil {
		a.failed = err
		result = storageResponse{Error: "storage_failed"}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > StorageRPCBytes || !m.Memory().Write(out, encoded) {
		a.failed = errStorageInvalid
		return 0
	}
	return uint32(len(encoded))
}

func (a *storageAction) commit(ctx context.Context) error {
	if a.failed != nil {
		return a.failed
	}
	tx, err := a.store.repository.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := a.authorize(ctx, tx, len(a.pending) > 0); err != nil {
		return err
	}
	if err := a.unchanged(ctx, tx); err != nil {
		return err
	}
	if err := a.commitShared(ctx, tx); err != nil {
		return err
	}
	if len(a.pending) == 0 {
		return tx.Commit()
	}
	if err := a.store.repository.writePrivate(ctx, tx, a.plugin, a.principal, a.pending); err != nil {
		return err
	}
	return tx.Commit()
}
