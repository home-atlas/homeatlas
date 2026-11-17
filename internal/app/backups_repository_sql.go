package app

import (
	"context"
	"database/sql"
	"errors"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type backupRepository struct{ db *sql.DB }

func (s *backupRepository) Begin(ctx context.Context) (unitofwork.Transaction, error) {
	return s.db.BeginTx(ctx, nil)
}

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

type unfinishedBackup struct{ id, home string }

func (s *backupRepository) interrupted(ctx context.Context) ([]unfinishedBackup, error) {
	if err := database.Apply(ctx, s.db, "app.backup-schedules", []database.Migration{{Version: 1, SQL: scheduleSchema}}); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,household_id FROM household_backup_history WHERE status='running'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []unfinishedBackup{}
	for rows.Next() {
		var v unfinishedBackup
		if err := rows.Scan(&v.id, &v.home); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *backupRepository) finishInterrupted(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE household_backup_history SET status='failed',error_code='backup_interrupted',finished_at=? WHERE status='running'", now)
	return err
}
func (b *backupRepository) loadSchedule(ctx context.Context, home string) (backupSchedule, error) {
	s := backupSchedule{household: home, IntervalHours: 24, Keep: 7}
	err := b.db.QueryRowContext(ctx, `SELECT enabled,interval_hours,keep_count,owner_id,member_id,sealed_password,next_run_at FROM household_backup_schedules WHERE household_id=?`, home).Scan(&s.Enabled, &s.IntervalHours, &s.Keep, &s.owner, &s.member, &s.sealed, &s.next)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	s.NextRun = backupTimestamp(s.next)
	s.PasswordConfigured = len(s.sealed) > 0
	return s, err
}

func (s *backupRepository) saveSchedule(ctx context.Context, tx unitofwork.Transaction, p access.Principal, enabled bool, interval, keep int, sealed []byte, next int64) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO household_backup_schedules VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(household_id) DO UPDATE SET enabled=excluded.enabled,interval_hours=excluded.interval_hours,keep_count=excluded.keep_count,owner_id=excluded.owner_id,member_id=excluded.member_id,sealed_password=excluded.sealed_password,next_run_at=excluded.next_run_at`, p.HouseholdID, enabled, interval, keep, p.UserID, p.MembershipID, sealed, next)
	return err
}
func (s *backupRepository) history(ctx context.Context, home string, offset int) ([]backupHistory, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM household_backup_history WHERE household_id=?", home).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,started_at,finished_at,trigger,status,error_code,size_bytes FROM household_backup_history WHERE household_id=? ORDER BY rowid DESC LIMIT 25 OFFSET ?`, home, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []backupHistory{}
	for rows.Next() {
		var h backupHistory
		var started, finished int64
		if err := rows.Scan(&h.ID, &started, &finished, &h.Trigger, &h.Status, &h.ErrorCode, &h.Size); err != nil {
			return nil, 0, err
		}
		h.StartedAt = backupTimestamp(started)
		h.FinishedAt = backupTimestamp(finished)
		out = append(out, h)
	}
	return out, total, rows.Err()
}
func (s *backupRepository) historyFile(ctx context.Context, home, id string) (string, int64, error) {
	var status string
	var size int64
	err := s.db.QueryRowContext(ctx, "SELECT status,size_bytes FROM household_backup_history WHERE household_id=? AND id=?", home, id).Scan(&status, &size)
	return status, size, database.Normalize(err)
}
func (s *backupRepository) dueSchedule(ctx context.Context, now int64) (string, error) {
	var home string
	err := s.db.QueryRowContext(ctx, "SELECT household_id FROM household_backup_schedules WHERE enabled=1 AND next_run_at<=? ORDER BY next_run_at,household_id LIMIT 1", now).Scan(&home)
	return home, err
}
func (s *backupRepository) startHistory(ctx context.Context, tx unitofwork.Transaction, id, home string, now int64, trigger string) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO household_backup_history(id,household_id,started_at,trigger,status) VALUES(?,?,?,?,'running')`, id, home, now, trigger)
	return err
}
func (s *backupRepository) advanceSchedule(ctx context.Context, tx unitofwork.Transaction, home string, next int64, disable bool) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	if disable {
		_, err = q.ExecContext(ctx, "UPDATE household_backup_schedules SET enabled=0,sealed_password=X'',next_run_at=0 WHERE household_id=?", home)
	} else {
		_, err = q.ExecContext(ctx, "UPDATE household_backup_schedules SET next_run_at=? WHERE household_id=?", next, home)
	}
	return err
}
func (s *backupRepository) finishHistory(ctx context.Context, id, home, status, code string, size, now int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE household_backup_history SET status=?,error_code=?,size_bytes=?,finished_at=? WHERE id=? AND household_id=?", status, code, size, now, id, home)
	return err
}
func (s *backupRepository) retentionFailed(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE household_backup_history SET error_code='backup_retention_failed' WHERE id=?", id)
	return err
}
func (s *backupRepository) expiredHistory(ctx context.Context, home string, keep int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM household_backup_history WHERE household_id=? AND status='ready' ORDER BY rowid DESC LIMIT -1 OFFSET ?", home, keep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *backupRepository) expireHistory(ctx context.Context, home, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE household_backup_history SET status='expired',error_code='' WHERE household_id=? AND id=? AND status='ready'", home, id)
	return err
}
func (s *backupRepository) trimHistory(ctx context.Context, home string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM household_backup_history WHERE household_id=? AND status IN ('failed','expired') AND id NOT IN (SELECT id FROM household_backup_history WHERE household_id=? ORDER BY rowid DESC LIMIT 200)`, home, home)
	return err
}
func (s *backupRepository) restoreCore(ctx context.Context, tx unitofwork.Transaction, snapshot householdSnapshot, principal access.Principal) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, "UPDATE identity_households SET name=?,timezone=? WHERE id=?", snapshot.household.Name, snapshot.household.Timezone, principal.HouseholdID); err != nil {
		return err
	}
	for _, part := range snapshot.parts {
		if _, err := q.ExecContext(ctx, "DELETE FROM household_module_states WHERE household_id=? AND module_id=?", principal.HouseholdID, part.ID); err != nil {
			return err
		}
		if enabled, ok := snapshot.modules[part.ID]; ok {
			if _, err := q.ExecContext(ctx, "INSERT INTO household_module_states(household_id,module_id,enabled) VALUES(?,?,?)", principal.HouseholdID, part.ID, enabled); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *backupRepository) emptyInstallation(ctx context.Context, tx unitofwork.Transaction, participants []backup.Participant) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	var occupied bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM household_module_states)").Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return identity.ErrInstallationNotEmpty
	}
	for _, p := range participants {
		present, err := p.Present(ctx, q)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		empty, err := p.Empty(ctx, q)
		if err != nil {
			return err
		}
		if !empty {
			return identity.ErrInstallationNotEmpty
		}
	}
	return nil
}
func (s *backupRepository) applyPlan(ctx context.Context, tx unitofwork.Transaction, plan backup.Plan, rc backup.RestoreContext) error {
	raw, err := database.Transaction(tx)
	if err != nil {
		return err
	}
	return plan.Apply(ctx, raw, rc)
}
