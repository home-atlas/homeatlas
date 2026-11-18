package application

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/access"
	backup "github.com/home-atlas/homeatlas/internal/core/backup/stream"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Store struct {
	repository Repository
	path       string
}

// A membership is part of the namespace: removing and reinviting an account must
// not make its historical private records accessible to the new membership.
// Restores may leave a large, reusable WAL plus the main database in cache.
// SQLite owns durability; advice only discards clean pages and never syncs,
// truncates or changes journal settings. Unsupported filesystems may ignore it.
func (s *Store) DiscardCache() {
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

type Action struct {
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
	sharedPending    map[string]SharedChange
	grantsPending    map[GrantKey]*SharedGrant
}
type ActionKey struct{}

func (s *Store) Begin(ctx context.Context, p *Package, readOnly bool) (*Action, error) {
	principal, ok := access.Current(ctx)
	if !ok || !storageIdentity.MatchString(principal.MembershipID) {
		return nil, ErrStorageDenied
	}
	a := &Action{store: s, plugin: p.Manifest.ID, principal: principal, readOnly: readOnly, pending: map[string]*string{}}
	if err := a.authorize(ctx, nil, false); err != nil {
		return nil, err
	}
	revision, err := a.currentRevision(ctx, nil)
	a.revision = revision
	if err == nil && p.Manifest.Storage == "shared" {
		a.shared = true
		a.sharedPending = map[string]SharedChange{}
		a.grantsPending = map[GrantKey]*SharedGrant{}
		a.sharedRevision, err = a.currentSharedRevision(ctx, nil)
	}
	return a, err
}

// Recheck live permissions and activation at storage boundaries and commit.
// Nothing supplied by the guest participates in these namespace parameters.
func (a *Action) authorize(ctx context.Context, tx unitofwork.Transaction, write bool) error {
	if write && a.readOnly {
		return ErrStorageDenied
	}
	allowed, err := a.store.repository.Allowed(ctx, tx, a.plugin, a.principal, write)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrStorageDenied
	}
	return nil
}
func (a *Action) currentRevision(ctx context.Context, tx unitofwork.Transaction) (int64, error) {
	revision, owner, err := a.store.repository.Revision(ctx, tx, a.plugin, a.principal)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return 0, nil
	}
	if err == nil && owner != a.principal.UserID {
		return 0, ErrStorageDenied
	}
	return revision, err
}
func (a *Action) unchanged(ctx context.Context, tx unitofwork.Transaction) error {
	revision, err := a.currentRevision(ctx, tx)
	if err != nil {
		return err
	}
	if revision != a.revision {
		return ErrStorageConflict
	}
	return nil
}

func (a *Action) Call(ctx context.Context, req StorageRequest) (StorageResponse, error) {
	a.calls++
	if a.failed != nil {
		return StorageResponse{}, a.failed
	}
	if a.calls > StorageCalls {
		return StorageResponse{}, errStorageInvalid
	}
	if strings.HasPrefix(req.Op, "shared.") {
		return a.sharedCall(ctx, req)
	}
	if req.User != "" || req.Permission != "" {
		return StorageResponse{}, errStorageInvalid
	}
	write := req.Op == "set" || req.Op == "delete"
	if err := a.authorize(ctx, nil, write); err != nil {
		return StorageResponse{}, err
	}
	if err := a.unchanged(ctx, nil); err != nil {
		return StorageResponse{}, err
	}
	if req.Op == "list" {
		if req.Key != "" || req.Value != nil || (req.After != "" && !storageKey.MatchString(req.After)) || req.Limit < 0 || req.Limit > StoragePageKeys {
			return StorageResponse{}, errStorageInvalid
		}
		limit := req.Limit
		if limit == 0 {
			limit = StoragePageKeys
		}
		keys, err := a.store.repository.Keys(ctx, a.plugin, a.principal, req.After, limit+len(a.pending)+1)
		if err != nil {
			return StorageResponse{}, err
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
		result := StorageResponse{Keys: ordered, OK: true}
		if len(ordered) > limit {
			result.Keys = ordered[:limit]
			result.Next = result.Keys[limit-1]
		}
		return result, nil
	}
	if !storageKey.MatchString(req.Key) || req.After != "" || req.Limit != 0 {
		return StorageResponse{}, errStorageInvalid
	}
	switch req.Op {
	case "get":
		if req.Value != nil {
			return StorageResponse{}, errStorageInvalid
		}
		if value, exists := a.pending[req.Key]; exists {
			return StorageResponse{Found: value != nil, Value: value, OK: true}, nil
		}
		value, err := a.store.repository.Value(ctx, a.plugin, a.principal, req.Key)
		if errors.Is(err, unitofwork.ErrNotFound) {
			return StorageResponse{OK: true}, nil
		}
		if err != nil {
			return StorageResponse{}, err
		}
		return StorageResponse{Found: true, Value: &value, OK: true}, nil
	case "set":
		if req.Value == nil || len(*req.Value) > StorageValueBytes || !utf8.ValidString(*req.Value) {
			return StorageResponse{}, errStorageInvalid
		}
		a.pending[req.Key] = req.Value
		return StorageResponse{OK: true}, nil
	case "delete":
		if req.Value != nil {
			return StorageResponse{}, errStorageInvalid
		}
		a.pending[req.Key] = nil
		return StorageResponse{OK: true}, nil
	default:
		return StorageResponse{}, errStorageInvalid
	}
}

// call is the only host function. Failures poison the action even if guest code
// ignores them. The output buffer is validated before staging any mutation.
func (a *Action) Commit(ctx context.Context) error {
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
	if err := a.store.repository.WritePrivate(ctx, tx, a.plugin, a.principal, a.pending); err != nil {
		return err
	}
	return tx.Commit()
}

func NewStore(repository Repository, path string) *Store {
	return &Store{repository: repository, path: path}
}
func WithAction(ctx context.Context, action *Action) context.Context {
	return context.WithValue(ctx, ActionKey{}, action)
}
func ActionFrom(ctx context.Context) (*Action, bool) {
	a, ok := ctx.Value(ActionKey{}).(*Action)
	return a, ok
}
func (a *Action) Fail(err error) { a.failed = err }
func (a *Action) Failure() error { return a.failed }
