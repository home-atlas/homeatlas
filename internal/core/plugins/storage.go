package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
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
	db   *sql.DB
	path string
}

// A membership is part of the namespace: removing and reinviting an account must
// not make its historical private records accessible to the new membership.
func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	err := database.Apply(ctx, db, "core.plugin-storage", []database.Migration{{Version: 1, SQL: `
 CREATE TABLE plugin_storage_scopes (
   plugin_id TEXT NOT NULL, household_id TEXT NOT NULL, member_id TEXT NOT NULL,
   user_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0),
   PRIMARY KEY(plugin_id,household_id,member_id)) WITHOUT ROWID;
 CREATE TABLE plugin_storage_records (
   plugin_id TEXT NOT NULL, household_id TEXT NOT NULL, member_id TEXT NOT NULL,
   key TEXT NOT NULL CHECK(length(CAST(key AS BLOB)) BETWEEN 1 AND 128),
   value TEXT NOT NULL CHECK(length(CAST(value AS BLOB))<=16384),
   PRIMARY KEY(plugin_id,household_id,member_id,key),
   FOREIGN KEY(plugin_id,household_id,member_id)
     REFERENCES plugin_storage_scopes(plugin_id,household_id,member_id) ON DELETE CASCADE
 ) WITHOUT ROWID;`}, {Version: 2, SQL: `
 CREATE TABLE plugin_shared_scopes(plugin_id TEXT NOT NULL,household_id TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision>0),PRIMARY KEY(plugin_id,household_id)) WITHOUT ROWID;
 CREATE TABLE plugin_shared_records(
   plugin_id TEXT NOT NULL,household_id TEXT NOT NULL,key TEXT NOT NULL,record_id TEXT NOT NULL,
   owner_user_id TEXT NOT NULL,owner_member_id TEXT NOT NULL,value TEXT NOT NULL CHECK(length(CAST(value AS BLOB))<=16384),
   PRIMARY KEY(plugin_id,household_id,key),UNIQUE(plugin_id,household_id,record_id),UNIQUE(plugin_id,household_id,key,record_id)) WITHOUT ROWID;
 CREATE INDEX plugin_shared_owner ON plugin_shared_records(plugin_id,household_id,owner_user_id,owner_member_id,key);
 CREATE TABLE plugin_shared_grants(
   plugin_id TEXT NOT NULL,household_id TEXT NOT NULL,record_id TEXT NOT NULL,key TEXT NOT NULL,
   user_id TEXT NOT NULL,member_id TEXT NOT NULL,permission TEXT NOT NULL CHECK(permission IN ('read','write')),
   PRIMARY KEY(plugin_id,household_id,record_id,user_id),
   FOREIGN KEY(plugin_id,household_id,key,record_id) REFERENCES plugin_shared_records(plugin_id,household_id,key,record_id) ON DELETE CASCADE) WITHOUT ROWID;
 CREATE INDEX plugin_shared_recipient ON plugin_shared_grants(plugin_id,household_id,user_id,member_id,key);`}})
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			return nil, err
		}
		if name == "main" {
			store.path = path
		}
	}
	return store, rows.Err()
}

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
type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type storageActionKey struct{}

func (s *Store) begin(ctx context.Context, p *Package, readOnly bool) (*storageAction, error) {
	principal, ok := access.Current(ctx)
	if !ok || !storageIdentity.MatchString(principal.MembershipID) {
		return nil, ErrStorageDenied
	}
	a := &storageAction{store: s, plugin: p.Manifest.ID, principal: principal, readOnly: readOnly, pending: map[string]*string{}}
	if err := a.authorize(ctx, s.db, false); err != nil {
		return nil, err
	}
	revision, err := a.currentRevision(ctx, s.db)
	a.revision = revision
	if err == nil && p.Manifest.Storage == "shared" {
		a.shared = true
		a.sharedPending = map[string]sharedChange{}
		a.grantsPending = map[grantKey]*sharedGrant{}
		a.sharedRevision, err = a.currentSharedRevision(ctx, s.db)
	}
	return a, err
}

// Recheck live permissions and activation at storage boundaries and commit.
// Nothing supplied by the guest participates in these namespace parameters.
func (a *storageAction) authorize(ctx context.Context, q rowQueryer, write bool) error {
	if write && a.readOnly {
		return ErrStorageDenied
	}
	p := a.principal
	var allowed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM identity_memberships m
 JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id
 JOIN module_states s ON s.id=? AND s.enabled=1
 WHERE mi.id=? AND m.user_id=? AND m.household_id=?
 AND COALESCE((SELECT enabled FROM household_module_states WHERE household_id=m.household_id AND module_id=s.id),1)=1
 AND EXISTS(SELECT 1 FROM identity_permissions WHERE user_id=m.user_id AND household_id=m.household_id AND capability=?)
 AND (?=0 OR EXISTS(SELECT 1 FROM identity_permissions WHERE user_id=m.user_id AND household_id=m.household_id AND capability=?)))`,
		a.plugin, p.MembershipID, p.UserID, p.HouseholdID, a.plugin+".read", write, a.plugin+".write").Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrStorageDenied
	}
	return nil
}
func (a *storageAction) currentRevision(ctx context.Context, q rowQueryer) (int64, error) {
	var revision int64
	var owner string
	err := q.QueryRowContext(ctx, `SELECT revision,user_id FROM plugin_storage_scopes WHERE plugin_id=? AND household_id=? AND member_id=?`, a.plugin, a.principal.HouseholdID, a.principal.MembershipID).Scan(&revision, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err == nil && owner != a.principal.UserID {
		return 0, ErrStorageDenied
	}
	return revision, err
}
func (a *storageAction) unchanged(ctx context.Context, q rowQueryer) error {
	revision, err := a.currentRevision(ctx, q)
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
	if err := a.authorize(ctx, a.store.db, write); err != nil {
		return storageResponse{}, err
	}
	if err := a.unchanged(ctx, a.store.db); err != nil {
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
		rows, err := a.store.db.QueryContext(ctx, `SELECT key FROM plugin_storage_records WHERE plugin_id=? AND household_id=? AND member_id=? AND key>? ORDER BY key LIMIT ?`, a.plugin, a.principal.HouseholdID, a.principal.MembershipID, req.After, limit+len(a.pending)+1)
		if err != nil {
			return storageResponse{}, err
		}
		keys := map[string]bool{}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return storageResponse{}, err
			}
			keys[key] = true
		}
		err = rows.Err()
		rows.Close()
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
		var value string
		err := a.store.db.QueryRowContext(ctx, `SELECT value FROM plugin_storage_records WHERE plugin_id=? AND household_id=? AND member_id=? AND key=?`, a.plugin, a.principal.HouseholdID, a.principal.MembershipID, req.Key).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
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
	tx, err := a.store.db.BeginTx(ctx, nil)
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
	p := a.principal
	if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_storage_scopes VALUES(?,?,?,?,1) ON CONFLICT(plugin_id,household_id,member_id) DO UPDATE SET revision=revision+1`, a.plugin, p.HouseholdID, p.MembershipID, p.UserID); err != nil {
		return err
	}
	for key, value := range a.pending {
		if value == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM plugin_storage_records WHERE plugin_id=? AND household_id=? AND member_id=? AND key=?`, a.plugin, p.HouseholdID, p.MembershipID, key)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO plugin_storage_records VALUES(?,?,?,?,?) ON CONFLICT(plugin_id,household_id,member_id,key) DO UPDATE SET value=excluded.value`, a.plugin, p.HouseholdID, p.MembershipID, key, *value)
		}
		if err != nil {
			return fmt.Errorf("commit plugin records: %w", err)
		}
	}
	return tx.Commit()
}
