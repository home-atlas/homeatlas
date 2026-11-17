package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
	"golang.org/x/sync/semaphore"
)

const backupPermission = "household.backup"
const maxBackupBytes int64 = 1 << 30

// Slow storage can outlast the normal HTTP response deadline. Keep a finite
// deadline for authorized backup work without relaxing request-body limits.
func backupResponseDeadline(w http.ResponseWriter) error {
	return responseDeadline(w, 10*time.Minute)
}
func responseDeadline(w http.ResponseWriter, budget time.Duration) error {
	err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(budget))
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// Streamed on disk; plugin records can exceed the former metadata-only bound.
const maxSnapshotBytes int64 = 128 << 20
const maxBackupFiles = 10000

// One permit per request; taking all permits excludes readers during a restore.
const backupLockCapacity int64 = 1 << 60

var backupKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var errBackupDenied = errors.New("backup denied")
var errInvalidBackup = backup.Invalid
var errBackupConflict = backup.Conflict
var errBackupModuleRequired = backup.ModuleRequired
var errBackupOriginal = backup.OriginalUnavailable

// Core tables are independent of module schemas; v1-v3 retain a declared legacy adapter.
var backupSchema = []string{
	`CREATE TABLE household (id TEXT PRIMARY KEY, name TEXT NOT NULL, timezone TEXT NOT NULL)`,
	`CREATE TABLE modules (id TEXT PRIMARY KEY, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)))`,
}

const backupMembersSchema = `CREATE TABLE members (id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, role TEXT NOT NULL, member_id TEXT NOT NULL UNIQUE, permissions TEXT NOT NULL)`

const backupMembersSchemaV3 = `CREATE TABLE members (id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL, member_id TEXT NOT NULL UNIQUE, permissions TEXT NOT NULL, username TEXT NOT NULL UNIQUE, assisted INTEGER NOT NULL CHECK(assisted IN (0,1)))`

type snapshotPart struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	contents backup.Contents
}
type householdSnapshot struct {
	coreOnly  bool
	version   int
	members   []identity.BackupMember
	household identity.Household
	modules   map[string]bool
	parts     []snapshotPart
	path      string
}

func (s householdSnapshot) unavailableCount() int {
	n := 0
	for _, part := range s.parts {
		n += len(part.contents.Unavailable)
	}
	return n
}

type backupManifest struct {
	CoreOnly             bool              `json:"coreOnly,omitempty"`
	Format               string            `json:"format"`
	Version              int               `json:"version"`
	HouseholdID          string            `json:"householdId"`
	CreatedAt            string            `json:"createdAt"`
	SHA256               map[string]string `json:"sha256"`
	UnavailableOriginals []string          `json:"unavailableOriginals,omitempty"`
	Modules              []snapshotPart    `json:"modules,omitempty"`
}
type householdBackups struct {
	participants    []backup.Participant
	store           *backupRepository
	accounts        *identity.Service
	dataDir         string
	resources       *semaphore.Weighted
	operations      sync.Mutex
	mutations       *semaphore.Weighted
	readOnlyActions map[string]bool
}

// Exports freeze mutations but permit readers. Restores exclude all operations.
func (b *householdBackups) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// JSON handlers accept at most 16 KiB. Buffer one extra byte so their
		// existing limits reject oversized input without reading under a lock.
		contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if r.Body != nil && contentType == "application/json" && r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			body, err := io.ReadAll(io.LimitReader(r.Body, (16<<10)+1))
			if err != nil {
				backupFail(w, 400, "invalid_request")
				return
			}
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}
		}
		export := r.Method == "POST" && (r.URL.Path == "/api/household/backups" || r.URL.Path == "/api/household/backups/schedule/run")
		exclusive := (r.Method == "POST" && (r.URL.Path == "/api/household/restore" || r.URL.Path == "/api/setup/restore" || r.URL.Path == "/api/household/storage/move")) || (r.Method == "PUT" && r.URL.Path == "/api/household/backups/schedule")
		if export || exclusive {
			if !b.operations.TryLock() {
				backupFail(w, 423, "household_busy")
				return
			}
			defer b.operations.Unlock()
			if exclusive {
				if !b.resources.TryAcquire(backupLockCapacity) {
					backupFail(w, 423, "household_busy")
					return
				}
				defer b.resources.Release(backupLockCapacity)
			} else {
				if !b.resources.TryAcquire(1) {
					backupFail(w, 423, "household_busy")
					return
				}
				defer b.resources.Release(1)
				if !b.mutations.TryAcquire(1) {
					backupFail(w, 423, "household_busy")
					return
				}
				defer b.mutations.Release(1)
			}
		} else {
			waited := false
			if !b.resources.TryAcquire(1) {
				waited = true
				if err := b.resources.Acquire(r.Context(), 1); err != nil {
					backupFail(w, 408, "request_cancelled")
					return
				}
			}
			defer b.resources.Release(1)
			if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !b.readOnlyActions[r.Method+" "+r.URL.Path] {
				if !b.mutations.TryAcquire(1) {
					waited = true
					if err := b.mutations.Acquire(r.Context(), 1); err != nil {
						backupFail(w, 408, "request_cancelled")
						return
					}
				}
				defer b.mutations.Release(1)
			}
			// Queue time must not consume the normal server's response budget.
			// Authentication still runs below; read/body deadlines stay unchanged.
			if waited {
				if err := responseDeadline(w, 2*time.Minute); err != nil {
					backupError(w, err)
					return
				}
			}
		}
		if r.Context().Err() != nil {
			backupFail(w, 408, "request_cancelled")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (b *householdBackups) mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/household/backups", b.export)
	mux.HandleFunc("POST /api/household/restore", b.restore)
	mux.HandleFunc("GET /api/household/backups/previous", b.previous)
	mux.HandleFunc("POST /api/setup/restore", b.recoverInstallation)
	mux.HandleFunc("GET /api/household/backups/schedule", b.getSchedule)
	mux.HandleFunc("PUT /api/household/backups/schedule", b.saveSchedule)
	mux.HandleFunc("POST /api/household/backups/schedule/run", b.runScheduledNow)
	mux.HandleFunc("GET /api/household/backups/history", b.history)
	mux.HandleFunc("GET /api/household/backups/history/{id}/download", b.downloadHistory)
}
func backupFail(w http.ResponseWriter, status int, code string) {
	modules.JSON(w, status, map[string]string{"error": code})
}
func (b *householdBackups) authorize(ctx context.Context, tx unitofwork.Transaction) (access.Principal, error) {
	p, ok := access.Current(ctx)
	if !ok {
		return p, errBackupDenied
	}
	m, err := b.accounts.Member(ctx, tx, p.HouseholdID, p.UserID)
	if err != nil {
		return p, err
	}
	if m.MembershipID != p.MembershipID || m.Role != "owner" || !slices.Contains(m.Permissions, identity.Manage) || !slices.Contains(m.Permissions, backupPermission) {
		return p, errBackupDenied
	}
	return p, nil
}
func (b *householdBackups) allowed(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := access.Current(r.Context()); !ok {
		backupFail(w, 401, "authentication_required")
		return false
	}
	_, err := b.authorize(r.Context(), nil)
	if err != nil {
		if errors.Is(err, errBackupDenied) || errors.Is(err, unitofwork.ErrNotFound) {
			backupFail(w, 403, "permission_denied")
		} else {
			backupFail(w, 503, "backup_unavailable")
		}
		return false
	}
	return true
}
func backupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBackupModuleRequired):
		backupFail(w, 409, "backup_module_required")
	case errors.Is(err, errScheduleKey):
		backupFail(w, 503, "backup_schedule_key_unavailable")
	case errors.Is(err, errBackupPasswordRequired):
		backupFail(w, 400, "backup_password_required")
	case errors.Is(err, errBackupPasswordInvalid):
		backupFail(w, 400, "invalid_backup_password")
	case errors.Is(err, errBackupDecrypt):
		backupFail(w, 400, "backup_decryption_failed")
	case errors.Is(err, errBackupTooLarge):
		backupFail(w, 413, "backup_too_large")
	case errors.Is(err, errBackupDenied):
		backupFail(w, 403, "permission_denied")
	case errors.Is(err, errBackupConflict):
		backupFail(w, 409, "backup_household_conflict")
	case errors.Is(err, errInvalidBackup):
		backupFail(w, 400, "invalid_backup")
	case errors.Is(err, errBackupOriginal):
		backupFail(w, 409, "backup_original_unavailable")
	case errors.Is(err, identity.ErrInstallationNotEmpty):
		backupFail(w, 409, "setup_completed")
	case errors.Is(err, identity.ErrInvalidSetupCode):
		backupFail(w, 403, "invalid_setup_code")
	case errors.Is(err, identity.ErrRecoveryMembers):
		backupFail(w, 400, "invalid_backup")
	default:
		backupFail(w, 503, "backup_unavailable")
	}
}

func (b *householdBackups) temp() (string, error) {
	return os.MkdirTemp(b.dataDir, ".household-backup-")
}
func serveBackup(w http.ResponseWriter, r *http.Request, path, name string) {
	f, err := os.Open(path)
	if err != nil {
		backupFail(w, 404, "backup_not_found")
		return
	}
	defer f.Close()
	defer backup.DiscardCache(f)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, time.Time{}, backup.StreamReader(f))
}
func (b *householdBackups) export(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w, r) {
		return
	}
	if err := backupResponseDeadline(w); err != nil {
		backupError(w, err)
		return
	}
	password, err := backupPassword(r)
	if err == nil {
		err = requireBackupPassword(password)
	}
	if err != nil {
		backupError(w, err)
		return
	}
	version := 1
	if v := r.Header.Get("X-HomeAtlas-Backup-Version"); v != "" {
		if v != "2" && v != "3" && v != "4" {
			backupFail(w, 400, "invalid_backup_version")
			return
		}
		version = 2
		if v == "3" {
			version = 3
		}
		if v == "4" {
			version = 4
		}
	}
	dir, err := b.temp()
	if err != nil {
		backupError(w, err)
		return
	}
	defer os.RemoveAll(dir)
	path, _, err := b.create(r.Context(), dir, false, version)
	if err != nil {
		backupError(w, err)
		return
	}
	path, err = encryptBackup(r.Context(), path, path+".age", password)
	if err != nil {
		backupError(w, err)
		return
	}
	prefix := "homeatlas-household-v1-"
	if version >= 2 {
		prefix = "homeatlas-complete-"
	}
	serveBackup(w, r, path, prefix+time.Now().UTC().Format("20060102-150405")+".zip.age")
}
func (b *householdBackups) previousPath(household string) string {
	return filepath.Join(b.dataDir, "backups", household+"-before-restore.zip")
}
func (b *householdBackups) previous(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w, r) {
		return
	}
	if err := backupResponseDeadline(w); err != nil {
		backupError(w, err)
		return
	}
	p, _ := access.Current(r.Context())
	if !backupKey.MatchString(p.HouseholdID) {
		backupError(w, errInvalidBackup)
		return
	}
	path := b.previousPath(p.HouseholdID)
	f, err := os.Open(path)
	if err != nil {
		backupFail(w, 404, "backup_not_found")
		return
	}
	prefix := make([]byte, len(agePrefix))
	_, readErr := f.Read(prefix)
	f.Close()
	if readErr != nil || string(prefix) != agePrefix {
		// Legacy safety copies are downloaded encrypted too; the historical file stays private until replaced.
		password, err := backupPassword(r)
		if err == nil {
			err = requireBackupPassword(password)
		}
		if err != nil {
			backupError(w, err)
			return
		}
		dir, err := b.temp()
		if err != nil {
			backupError(w, err)
			return
		}
		defer os.RemoveAll(dir)
		path, err = encryptBackup(r.Context(), path, filepath.Join(dir, "previous.zip.age"), password)
		if err != nil {
			backupError(w, err)
			return
		}
	}
	serveBackup(w, r, path, "homeatlas-before-restore.zip.age")
}

func (b *householdBackups) restore(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w, r) {
		return
	}
	if r.Header.Get("X-HomeAtlas-Confirm-Restore") != "replace-household" {
		backupFail(w, 400, "restore_confirmation_required")
		return
	}
	if err := backupResponseDeadline(w); err != nil {
		backupError(w, err)
		return
	}
	dir, err := b.temp()
	if err != nil {
		backupError(w, err)
		return
	}
	defer os.RemoveAll(dir)
	path, password, err := receiveBackup(w, r, dir)
	if err == nil {
		err = requireBackupPassword(password)
	}
	if err != nil {
		backupError(w, err)
		return
	}
	p, _ := access.Current(r.Context())
	s, err := b.extractBackup(r.Context(), path, filepath.Join(dir, "validated"), p.HouseholdID)
	if err != nil {
		backupError(w, err)
		return
	}
	before, err := os.MkdirTemp(dir, "before-")
	if err != nil {
		backupError(w, err)
		return
	}
	previous, previousMissing, err := b.create(r.Context(), before, true, s.version)
	if err != nil {
		backupError(w, err)
		return
	}
	previous, err = encryptBackup(r.Context(), previous, previous+".age", password)
	if err != nil {
		backupError(w, err)
		return
	}
	if err = os.MkdirAll(filepath.Join(b.dataDir, "backups"), 0700); err != nil {
		backupError(w, err)
		return
	}
	if !backupKey.MatchString(p.HouseholdID) {
		backupError(w, errInvalidBackup)
		return
	}
	if err = os.Rename(previous, b.previousPath(p.HouseholdID)); err != nil {
		backupError(w, err)
		return
	}
	if err = syncDirectory(filepath.Join(b.dataDir, "backups")); err != nil {
		backupError(w, err)
		return
	}
	if err = b.apply(r.Context(), s, filepath.Join(dir, "validated"), previousMissing > 0, nil); err != nil {
		backupError(w, err)
		return
	}
	report := b.restoreReport(s)
	report["previousBackupAvailable"] = true
	report["previousUnavailableOriginals"] = previousMissing
	modules.JSON(w, 200, report)
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (b *householdBackups) restoreReport(s householdSnapshot) map[string]any {
	counts := map[string]int{}
	report := map[string]any{"restored": true, "unavailableOriginals": s.unavailableCount(), "moduleRecords": counts}
	for _, p := range b.participants {
		if p.LegacyCountField != "" {
			report[p.LegacyCountField] = 0
		}
	}
	for _, part := range s.parts {
		counts[part.ID] = part.contents.Records
		if p, ok := b.snapshotParticipant(part); ok && p.LegacyCountField != "" {
			report[p.LegacyCountField] = part.contents.Records
		}
	}
	return report
}

func (b *householdBackups) apply(ctx context.Context, s householdSnapshot, dir string, keepOld bool, initialize func(unitofwork.Transaction) (access.Principal, error)) error {
	plans, err := b.stageRestore(ctx, s, dir)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		for i := len(plans) - 1; i >= 0; i-- {
			plans[i].Finish(committed, keepOld)
		}
	}()
	tx, err := b.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	authorize := func(tx unitofwork.Transaction) (access.Principal, error) { return b.authorize(ctx, tx) }
	if initialize != nil {
		authorize = initialize
	}
	principal, err := authorize(tx)
	if err != nil {
		return err
	}
	if principal.HouseholdID != s.household.ID {
		return errBackupConflict
	}
	rc := backup.RestoreContext{HouseholdID: principal.HouseholdID}
	if initialize != nil {
		rc.RecoveryMemberships = map[string]string{}
		for _, m := range s.members {
			rc.RecoveryMemberships[m.ID] = m.MembershipID
		}
	}
	for _, plan := range plans {
		if err := b.store.applyPlan(ctx, tx, plan, rc); err != nil {
			return err
		}
	}
	if err := b.store.restoreCore(ctx, tx, s, principal); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
