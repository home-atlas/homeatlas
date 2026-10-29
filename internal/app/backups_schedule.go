package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

const scheduleSchema = `
CREATE TABLE household_backup_schedules (
 household_id TEXT PRIMARY KEY, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 interval_hours INTEGER NOT NULL CHECK(interval_hours IN (24,168)), keep_count INTEGER NOT NULL CHECK(keep_count BETWEEN 1 AND 30),
 owner_id TEXT NOT NULL, member_id TEXT NOT NULL, sealed_password BLOB NOT NULL, next_run_at INTEGER NOT NULL
);
CREATE TABLE household_backup_history (
 id TEXT PRIMARY KEY, household_id TEXT NOT NULL, started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0,
 trigger TEXT NOT NULL, status TEXT NOT NULL, error_code TEXT NOT NULL DEFAULT '', size_bytes INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX household_backup_history_home ON household_backup_history(household_id,started_at DESC,id DESC);`

type backupSchedule struct {
	Enabled                  bool   `json:"enabled"`
	IntervalHours            int    `json:"intervalHours"`
	Keep                     int    `json:"keep"`
	NextRun                  string `json:"nextRunAt,omitempty"`
	PasswordConfigured       bool   `json:"passwordConfigured"`
	household, owner, member string
	sealed                   []byte
	next                     int64
}
type backupHistory struct {
	ID         string `json:"id"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Trigger    string `json:"trigger"`
	Status     string `json:"status"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Size       int64  `json:"sizeBytes"`
	Available  bool   `json:"available"`
}

func backupTimestamp(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}
func (b *householdBackups) prepareSchedules(ctx context.Context) error {
	if err := database.Apply(ctx, b.db, "app.backup-schedules", []database.Migration{{Version: 1, SQL: scheduleSchema}}); err != nil {
		return err
	}
	// A single process owns this directory. A persisted running row means the previous process stopped.
	rows, err := b.db.QueryContext(ctx, "SELECT id,household_id FROM household_backup_history WHERE status='running'")
	if err != nil {
		return err
	}
	type unfinished struct{ id, home string }
	var pending []unfinished
	for rows.Next() {
		var row unfinished
		if err = rows.Scan(&row.id, &row.home); err != nil {
			break
		}
		pending = append(pending, row)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, row := range pending {
		path, err := b.historyPath(row.home, row.id)
		if err != nil {
			return err
		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	_, err = b.db.ExecContext(ctx, "UPDATE household_backup_history SET status='failed',error_code='backup_interrupted',finished_at=? WHERE status='running'", time.Now().Unix())
	return err
}
func (b *householdBackups) loadSchedule(ctx context.Context, home string) (backupSchedule, error) {
	s := backupSchedule{household: home, IntervalHours: 24, Keep: 7}
	err := b.db.QueryRowContext(ctx, `SELECT enabled,interval_hours,keep_count,owner_id,member_id,sealed_password,next_run_at FROM household_backup_schedules WHERE household_id=?`, home).Scan(&s.Enabled, &s.IntervalHours, &s.Keep, &s.owner, &s.member, &s.sealed, &s.next)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	s.NextRun = backupTimestamp(s.next)
	s.PasswordConfigured = len(s.sealed) > 0
	return s, err
}
func (b *householdBackups) getSchedule(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w, r) {
		return
	}
	p, _ := access.Current(r.Context())
	s, err := b.loadSchedule(r.Context(), p.HouseholdID)
	if err != nil {
		backupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	modules.JSON(w, 200, s)
}
func (b *householdBackups) saveSchedule(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w, r) {
		return
	}
	var input struct {
		Enabled       bool `json:"enabled"`
		IntervalHours int  `json:"intervalHours"`
		Keep          int  `json:"keep"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.IntervalHours != 24 && input.IntervalHours != 168) || input.Keep < 1 || input.Keep > 30 {
		backupFail(w, 400, "invalid_backup_schedule")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil || !errors.Is(err, io.EOF) {
		backupFail(w, 400, "invalid_backup_schedule")
		return
	}
	if input.Enabled && r.Header.Get("X-HomeAtlas-Confirm-Schedule") != "enable-and-retain" {
		backupFail(w, 400, "backup_schedule_confirmation_required")
		return
	}
	p, _ := access.Current(r.Context())
	current, err := b.loadSchedule(r.Context(), p.HouseholdID)
	if err != nil {
		backupError(w, err)
		return
	}
	password, err := backupPassword(r)
	if err != nil {
		backupError(w, err)
		return
	}
	sealed := []byte{}
	next := int64(0)
	if input.Enabled {
		if password != "" {
			if err = requireBackupPassword(password); err == nil {
				sealed, err = b.sealSchedulePassword(p, password)
			}
		} else if current.Enabled && current.owner == p.UserID && current.member == p.MembershipID && len(current.sealed) > 0 {
			sealed = current.sealed
		} else {
			err = errBackupPasswordRequired
		}
		if err != nil {
			backupError(w, err)
			return
		}
		next = current.next
		if !current.Enabled || current.IntervalHours != input.IntervalHours || next == 0 {
			next = time.Now().UTC().Add(time.Duration(input.IntervalHours) * time.Hour).Unix()
		}
	}
	tx, err := b.db.BeginTx(r.Context(), nil)
	if err != nil {
		backupError(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = b.authorize(r.Context(), tx); err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO household_backup_schedules VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(household_id) DO UPDATE SET enabled=excluded.enabled,interval_hours=excluded.interval_hours,keep_count=excluded.keep_count,owner_id=excluded.owner_id,member_id=excluded.member_id,sealed_password=excluded.sealed_password,next_run_at=excluded.next_run_at`, p.HouseholdID, input.Enabled, input.IntervalHours, input.Keep, p.UserID, p.MembershipID, sealed, next)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		backupError(w, err)
		return
	}
	b.getSchedule(w, r)
}
func (b *householdBackups) historyPath(home, id string) (string, error) {
	if !backupKey.MatchString(home) || !backupKey.MatchString(id) {
		return "", errInvalidBackup
	}
	return filepath.Join(b.dataDir, "backups", "scheduled", home, id+".zip.age"), nil
}
func (b *householdBackups) history(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w, r) {
		return
	}
	offset := 0
	if value := r.URL.Query().Get("offset"); value != "" {
		var err error
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 || offset > 100000 {
			backupFail(w, 400, "invalid_backup_offset")
			return
		}
	}
	p, _ := access.Current(r.Context())
	var total int
	if err := b.db.QueryRowContext(r.Context(), "SELECT count(*) FROM household_backup_history WHERE household_id=?", p.HouseholdID).Scan(&total); err != nil {
		backupError(w, err)
		return
	}
	rows, err := b.db.QueryContext(r.Context(), `SELECT id,started_at,finished_at,trigger,status,error_code,size_bytes FROM household_backup_history WHERE household_id=? ORDER BY rowid DESC LIMIT 25 OFFSET ?`, p.HouseholdID, offset)
	if err != nil {
		backupError(w, err)
		return
	}
	defer rows.Close()
	items := []backupHistory{}
	for rows.Next() {
		var h backupHistory
		var started, finished int64
		if err = rows.Scan(&h.ID, &started, &finished, &h.Trigger, &h.Status, &h.ErrorCode, &h.Size); err != nil {
			break
		}
		h.StartedAt = backupTimestamp(started)
		h.FinishedAt = backupTimestamp(finished)
		path, pathErr := b.historyPath(p.HouseholdID, h.ID)
		if h.Status == "ready" && pathErr == nil {
			info, statErr := os.Stat(path)
			h.Available = statErr == nil && info.Mode().IsRegular() && info.Size() == h.Size
		}
		items = append(items, h)
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		backupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	modules.JSON(w, 200, map[string]any{"items": items, "total": total})
}
func (b *householdBackups) downloadHistory(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w, r) {
		return
	}
	p, _ := access.Current(r.Context())
	id := r.PathValue("id")
	path, err := b.historyPath(p.HouseholdID, id)
	if err != nil {
		backupFail(w, 404, "backup_not_found")
		return
	}
	var status string
	var size int64
	err = b.db.QueryRowContext(r.Context(), "SELECT status,size_bytes FROM household_backup_history WHERE household_id=? AND id=?", p.HouseholdID, id).Scan(&status, &size)
	info, statErr := os.Stat(path)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (status != "ready" || statErr != nil || !info.Mode().IsRegular() || info.Size() != size)) {
		backupFail(w, 404, "backup_not_found")
		return
	}
	if err != nil {
		backupError(w, err)
		return
	}
	serveBackup(w, r, path, "homeatlas-complete-v2-"+id+".zip.age")
}
func (b *householdBackups) runScheduledNow(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w, r) {
		return
	}
	p, _ := access.Current(r.Context())
	s, err := b.loadSchedule(r.Context(), p.HouseholdID)
	if err != nil {
		backupError(w, err)
		return
	}
	if !s.Enabled {
		backupFail(w, 409, "backup_schedule_disabled")
		return
	}
	id, err := b.executeSchedule(r.Context(), s, "manual", time.Now().UTC())
	if err != nil {
		backupError(w, err)
		return
	}
	modules.JSON(w, 201, map[string]string{"id": id})
}

func (b *householdBackups) startSchedules(ctx context.Context) func() {
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			b.runDueSchedule(workerCtx, time.Now().UTC())
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}
func (b *householdBackups) runDueSchedule(ctx context.Context, now time.Time) {
	if ctx.Err() != nil {
		return
	}
	var home string
	err := b.db.QueryRowContext(ctx, "SELECT household_id FROM household_backup_schedules WHERE enabled=1 AND next_run_at<=? ORDER BY next_run_at,household_id LIMIT 1", now.Unix()).Scan(&home)
	if err != nil {
		return
	}
	// Idle polling must not make first-time setup or restore spuriously report household_busy.
	if !b.operations.TryLock() {
		return
	}
	defer b.operations.Unlock()
	if !b.mu.TryRLock() {
		return
	}
	defer b.mu.RUnlock()
	if !b.mutations.TryLock() {
		return
	}
	defer b.mutations.Unlock()
	s, err := b.loadSchedule(ctx, home)
	if err != nil || !s.Enabled || s.next > now.Unix() {
		return
	}
	b.executeSchedule(ctx, s, "scheduled", now)
}
func (b *householdBackups) executeSchedule(parent context.Context, s backupSchedule, trigger string, now time.Time) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	p := access.Principal{HouseholdID: s.household, UserID: s.owner, MembershipID: s.member}
	ctx = access.WithPrincipal(ctx, p)
	id := rand.Text()
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, authErr := b.authorize(ctx, tx)
	if authErr != nil && !errors.Is(authErr, errBackupDenied) && !errors.Is(authErr, sql.ErrNoRows) {
		return "", authErr
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO household_backup_history(id,household_id,started_at,trigger,status) VALUES(?,?,?,?,'running')`, id, s.household, now.Unix(), trigger); err != nil {
		return "", err
	}
	next := now.Add(time.Duration(s.IntervalHours) * time.Hour).Unix()
	if authErr != nil {
		_, err = tx.ExecContext(ctx, "UPDATE household_backup_schedules SET enabled=0,sealed_password=X'',next_run_at=0 WHERE household_id=?", s.household)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE household_backup_schedules SET next_run_at=? WHERE household_id=?", next, s.household)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return "", err
	}
	var size int64
	if authErr != nil {
		err = errBackupDenied
	} else {
		size, err = b.writeScheduledBackup(ctx, s, p, id)
	}
	// Persist the outcome even if the request or shutdown canceled the copy.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer finishCancel()
	status, code := "ready", ""
	if err != nil {
		status = "failed"
		code = scheduledErrorCode(err)
	}
	_, finishErr := b.db.ExecContext(finishCtx, "UPDATE household_backup_history SET status=?,error_code=?,size_bytes=?,finished_at=? WHERE id=? AND household_id=?", status, code, size, time.Now().Unix(), id, s.household)
	if finishErr != nil {
		if path, pathErr := b.historyPath(s.household, id); pathErr == nil {
			os.Remove(path)
		}
		return id, finishErr
	}
	if err != nil {
		b.trimHistory(finishCtx, s.household)
		return id, err
	}
	if retentionErr := b.retainScheduled(finishCtx, s); retentionErr != nil {
		b.db.ExecContext(finishCtx, "UPDATE household_backup_history SET error_code='backup_retention_failed' WHERE id=?", id)
	}
	return id, nil
}
func scheduledErrorCode(err error) string {
	switch {
	case errors.Is(err, errBackupDenied):
		return "permission_denied"
	case errors.Is(err, errBackupOriginal):
		return "backup_original_unavailable"
	case errors.Is(err, errBackupTooLarge):
		return "backup_too_large"
	case errors.Is(err, errScheduleKey):
		return "backup_schedule_key_unavailable"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "backup_interrupted"
	default:
		return "backup_unavailable"
	}
}
func (b *householdBackups) writeScheduledBackup(ctx context.Context, s backupSchedule, p access.Principal, id string) (int64, error) {
	password, err := b.openSchedulePassword(p, s.sealed)
	if err != nil {
		return 0, err
	}
	dir, err := b.temp()
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	path, _, err := b.create(ctx, dir, false, 4)
	if err != nil {
		return 0, err
	}
	encrypted, err := encryptBackup(ctx, path, path+".age", password)
	if err != nil {
		return 0, err
	}
	final, err := b.historyPath(s.household, id)
	if err != nil {
		return 0, err
	}
	if err = os.MkdirAll(filepath.Dir(final), 0700); err != nil {
		return 0, err
	}
	info, err := os.Stat(encrypted)
	if err != nil {
		return 0, err
	}
	if err = os.Rename(encrypted, final); err != nil {
		return 0, err
	}
	if err = syncDirectory(filepath.Dir(final)); err != nil {
		os.Remove(final)
		return 0, err
	}
	return info.Size(), nil
}
func (b *householdBackups) retainScheduled(ctx context.Context, s backupSchedule) error {
	rows, err := b.db.QueryContext(ctx, "SELECT id FROM household_backup_history WHERE household_id=? AND status='ready' ORDER BY rowid DESC LIMIT -1 OFFSET ?", s.household, s.Keep)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		path, pathErr := b.historyPath(s.household, id)
		if pathErr != nil {
			return pathErr
		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err = syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
		if _, err = b.db.ExecContext(ctx, "UPDATE household_backup_history SET status='expired',error_code='' WHERE household_id=? AND id=? AND status='ready'", s.household, id); err != nil {
			return err
		}
	}
	return b.trimHistory(ctx, s.household)
}
func (b *householdBackups) trimHistory(ctx context.Context, home string) error {
	// Keep retained files and the latest 200 attempts.
	_, err := b.db.ExecContext(ctx, `DELETE FROM household_backup_history WHERE household_id=? AND status IN ('failed','expired') AND id NOT IN (SELECT id FROM household_backup_history WHERE household_id=? ORDER BY rowid DESC LIMIT 200)`, home, home)
	return err
}
