package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/home-atlas/homeatlas/internal/app/testdata/legacydocuments"
	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

const scheduleTestOrigin = "http://127.0.0.1:8080"
const scheduleTestPassword = "Frase privada para copias 2026"

type scheduleFixture struct {
	b       *householdBackups
	db      *sql.DB
	handler http.Handler
	cookie  *http.Cookie
	p       access.Principal
	code    string
}

func newScheduleFixture(t *testing.T, setup bool) scheduleFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	registry, err := modules.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := identity.New(ctx, db, identity.Config{PublicURL: scheduleTestOrigin, SetupCodePath: filepath.Join(dir, "setup-code"), Modules: registry, OwnerCapabilities: []string{identity.Manage, backupPermission, documents.Read, documents.Write, documents.Share}, DelegableCapabilities: []string{documents.Read}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(accounts.Close)
	docs, err := documents.New(db, filepath.Join(dir, "documents"), accounts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { docs.Close() })
	if err = registry.Register(ctx, docs); err != nil {
		t.Fatal(err)
	}
	b, err := newBackups(ctx, db, accounts, dir, backup.Participant{Provider: documents.NewBackup(dir, accounts), LegacyPrefix: "documents", LegacyCountField: "documents"})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	b.mount(mux)
	accounts.Mount(mux)
	registry.Mount(mux)
	f := scheduleFixture{b: b, db: db, handler: securityHeaders(accounts.Middleware(b.guard(mux)))}
	raw, err := os.ReadFile(filepath.Join(dir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	f.code = strings.TrimSpace(string(raw))
	if !setup {
		return f
	}
	payload, _ := json.Marshal(map[string]string{"code": f.code, "name": "Cuenta de prueba", "householdName": "Hogar de prueba", "email": "owner@example.test", "password": "Cuenta de prueba segura 2026", "timezone": "America/Mexico_City"})
	w := f.request("POST", "/api/setup", payload, "", "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var state identity.State
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	f.cookie = w.Result().Cookies()[0]
	m, err := accounts.Member(ctx, nil, state.Session.Household.ID, state.Session.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.p = access.Principal{UserID: m.ID, MembershipID: m.MembershipID, HouseholdID: state.Session.Household.ID}
	content := []byte("Original privado del hogar de prueba\n")
	digest := sha256.Sum256(content)
	if err = os.WriteFile(filepath.Join(dir, "documents", "original"), content, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO documents(id,household_id,owner_id,title,original_filename,media_type,size_bytes,sha256,file_key) VALUES('document',?,?,'Manual privado','manual.txt','text/plain',?,?,'original')`, f.p.HouseholdID, f.p.UserID, len(content), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f scheduleFixture) request(method, path string, body []byte, password, confirm string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, scheduleTestOrigin+path, bytes.NewReader(body))
	r.Header.Set("Origin", scheduleTestOrigin)
	r.Header.Set("Content-Type", "application/json")
	if password != "" {
		r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(password)))
	}
	if confirm != "" {
		r.Header.Set("X-HomeAtlas-Confirm-Schedule", confirm)
	}
	if f.cookie != nil {
		r.AddCookie(f.cookie)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f scheduleFixture) enable(t *testing.T, keep int) backupSchedule {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"enabled": true, "intervalHours": 24, "keep": keep})
	w := f.request("PUT", "/api/household/backups/schedule", body, scheduleTestPassword, "enable-and-retain")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var s backupSchedule
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func (f scheduleFixture) history(t *testing.T) []backupHistory {
	t.Helper()
	w := f.request("GET", "/api/household/backups/history", nil, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page struct {
		Items []backupHistory `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page.Items
}
func (f scheduleFixture) due(t *testing.T, now time.Time) {
	t.Helper()
	if _, err := f.db.Exec("UPDATE household_backup_schedules SET next_run_at=? WHERE household_id=?", now.Add(-time.Hour).Unix(), f.p.HouseholdID); err != nil {
		t.Fatal(err)
	}
	f.b.runDueSchedule(context.Background(), now)
}
func TestScheduledBackupEncryptionRetentionAndBothRecoveryFlows(t *testing.T) {
	f := newScheduleFixture(t, true)
	ctx := context.Background()
	s := f.enable(t, 1)
	if !s.Enabled || !s.PasswordConfigured || s.NextRun == "" {
		t.Fatal(s)
	}
	saved, err := f.b.loadSchedule(ctx, f.p.HouseholdID)
	if err != nil || bytes.Contains(saved.sealed, []byte(scheduleTestPassword)) {
		t.Fatal("plaintext schedule password", err)
	}
	keyInfo, err := os.Stat(filepath.Join(f.b.dataDir, "backup-schedule.key"))
	if err != nil || keyInfo.Mode().Perm() != 0600 {
		t.Fatal("unsafe key file", err)
	}
	w := f.request("POST", "/api/household/backups/schedule/run", nil, "", "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	first := f.history(t)[0]
	if first.Status != "ready" || !first.Available || first.Trigger != "manual" {
		t.Fatal(first)
	}
	downloaded := f.request("GET", "/api/household/backups/history/"+first.ID+"/download", nil, "", "")
	if downloaded.Code != 200 || !bytes.HasPrefix(downloaded.Body.Bytes(), []byte(agePrefix)) || downloaded.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("download not encrypted", downloaded.Code)
	}
	firstPath, _ := f.b.historyPath(f.p.HouseholdID, first.ID)
	info, err := os.Stat(firstPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe archive", err)
	}
	dir := t.TempDir()
	zipPath, err := decryptBackup(ctx, firstPath, dir, scheduleTestPassword, false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.b.extractBackup(ctx, zipPath, filepath.Join(dir, "validated"), f.p.HouseholdID)
	if err != nil || len(snapshot.members) != 1 || len(snapshot.parts) != 1 || snapshot.parts[0].contents.Records != 1 {
		t.Fatal("not a complete recoverable snapshot", err)
	}
	snapshotDB, err := sql.Open("sqlite", filepath.Join(dir, "validated", "household.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = snapshotDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'household_backup_%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("schedule secrets exported", err)
	}
	snapshotDB.Close()
	target := newScheduleFixture(t, false)
	r := httptest.NewRequest("POST", scheduleTestOrigin+"/api/setup/restore", bytes.NewReader(downloaded.Body.Bytes()))
	r.Header.Set("Origin", scheduleTestOrigin)
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-HomeAtlas-Setup-Code", target.code)
	r.Header.Set("X-HomeAtlas-Confirm-Restore", "initialize-installation")
	r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(scheduleTestPassword)))
	recovered := httptest.NewRecorder()
	target.handler.ServeHTTP(recovered, r)
	if recovered.Code != 201 {
		t.Fatal("scheduled archive cannot recover a new server", recovered.Code, recovered.Body.String())
	}
	if err = target.db.QueryRow("SELECT count(*) FROM household_backup_schedules").Scan(&count); err != nil || count != 0 {
		t.Fatal("schedule revived in new server", err)
	}
	if _, err = os.Stat(filepath.Join(target.b.dataDir, "backup-schedule.key")); !os.IsNotExist(err) {
		t.Fatal("installation key imported")
	}
	if _, err = f.db.Exec("UPDATE documents SET title='Después'"); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", scheduleTestOrigin+"/api/household/restore", bytes.NewReader(downloaded.Body.Bytes()))
	r.AddCookie(f.cookie)
	r.Header.Set("Origin", scheduleTestOrigin)
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-HomeAtlas-Confirm-Restore", "replace-household")
	r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(scheduleTestPassword)))
	restored := httptest.NewRecorder()
	f.handler.ServeHTTP(restored, r)
	if restored.Code != 200 {
		t.Fatal("scheduled archive cannot restore current home", restored.Code, restored.Body.String())
	}
	before, _ := os.ReadFile(f.b.previousPath(f.p.HouseholdID))
	f.due(t, time.Now().UTC())
	history := f.history(t)
	if len(history) != 2 || history[0].Status != "ready" || history[1].Status != "expired" {
		t.Fatal("incorrect retention", history)
	}
	if _, err = os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatal("expired archive still retained", err)
	}
	if after, _ := os.ReadFile(f.b.previousPath(f.p.HouseholdID)); !bytes.Equal(after, before) {
		t.Fatal("retention touched restore safety copy")
	}
	if w = f.request("GET", "/api/household/backups/history/"+first.ID+"/download", nil, "", ""); w.Code != 404 {
		t.Fatal("expired copy downloadable")
	}
	body := []byte(`{"enabled":false,"intervalHours":24,"keep":1}`)
	if w = f.request("PUT", "/api/household/backups/schedule", body, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	disabled, _ := f.b.loadSchedule(ctx, f.p.HouseholdID)
	if disabled.Enabled || len(disabled.sealed) != 0 || disabled.next != 0 || !f.history(t)[0].Available {
		t.Fatal("disable removed archives or retained secret")
	}
	if w = f.request("POST", "/api/household/backups/schedule/run", nil, "", ""); w.Code != 409 {
		t.Fatal("disabled job ran")
	}
}
func TestScheduledBackupFailureKeepsLastGoodAndRevocationStopsAutomation(t *testing.T) {
	f := newScheduleFixture(t, true)
	f.enable(t, 1)
	now := time.Now().UTC()
	f.due(t, now)
	good := f.history(t)[0]
	if err := os.Remove(filepath.Join(f.b.dataDir, "documents", "original")); err != nil {
		t.Fatal(err)
	}
	f.due(t, now.Add(time.Hour))
	h := f.history(t)
	if len(h) != 2 || h[0].ErrorCode != "backup_original_unavailable" || h[0].Status != "failed" || h[1].ID != good.ID || !h[1].Available {
		t.Fatal("failed copy expired last good", h)
	}
	f.b.runDueSchedule(context.Background(), now.Add(time.Hour))
	if len(f.history(t)) != 2 {
		t.Fatal("missed intervals replayed repeatedly")
	}
	if err := os.Remove(filepath.Join(f.b.dataDir, "backup-schedule.key")); err != nil {
		t.Fatal(err)
	}
	f.due(t, now.Add(2*time.Hour))
	h = f.history(t)
	if h[0].ErrorCode != "backup_schedule_key_unavailable" || !h[2].Available {
		t.Fatal("lost key not safely reported", h)
	}
	if _, err := os.Stat(filepath.Join(f.b.dataDir, "backup-schedule.key")); !os.IsNotExist(err) {
		t.Fatal("missing key silently replaced")
	}
	if _, err := f.db.Exec("DELETE FROM identity_permissions WHERE user_id=? AND capability='household.backup'", f.p.UserID); err != nil {
		t.Fatal(err)
	}
	f.due(t, now.Add(3*time.Hour))
	s, _ := f.b.loadSchedule(context.Background(), f.p.HouseholdID)
	if s.Enabled || len(s.sealed) != 0 {
		t.Fatal("revoked principal kept automation")
	}
	var status, code string
	if err := f.db.QueryRow("SELECT status,error_code FROM household_backup_history ORDER BY rowid DESC LIMIT 1").Scan(&status, &code); err != nil || status != "failed" || code != "permission_denied" {
		t.Fatal("permission loss not recorded", err)
	}
	for _, path := range []string{"/api/household/backups/history", "/api/household/backups/schedule", "/api/household/backups/history/" + good.ID + "/download"} {
		if w := f.request("GET", path, nil, "", ""); w.Code != 403 {
			t.Fatal("revoked permission accepted", path, w.Code)
		}
	}
}
func TestScheduleValidationPermissionsCSRFAndPasswordBinding(t *testing.T) {
	f := newScheduleFixture(t, true)
	for _, body := range []string{`{}`, `{"enabled":true,"intervalHours":1,"keep":1}`, `{"enabled":true,"intervalHours":24,"keep":0}`, `{"enabled":true,"intervalHours":24,"keep":31}`, `{"enabled":true,"intervalHours":24,"keep":1,"householdId":"other"}`, `{"enabled":false,"intervalHours":24,"keep":1} {}`} {
		if w := f.request("PUT", "/api/household/backups/schedule", []byte(body), scheduleTestPassword, "enable-and-retain"); w.Code != 400 {
			t.Fatal("bad schedule accepted", body, w.Code)
		}
	}
	body := []byte(`{"enabled":true,"intervalHours":24,"keep":1}`)
	if w := f.request("PUT", "/api/household/backups/schedule", body, scheduleTestPassword, ""); w.Code != 400 {
		t.Fatal("confirmation absent")
	}
	if w := f.request("PUT", "/api/household/backups/schedule", body, "", "enable-and-retain"); w.Code != 400 {
		t.Fatal("first password absent")
	}
	unauth := f
	unauth.cookie = nil
	if w := unauth.request("GET", "/api/household/backups/history", nil, "", ""); w.Code != 401 {
		t.Fatal("history public")
	}
	r := httptest.NewRequest("PUT", scheduleTestOrigin+"/api/household/backups/schedule", bytes.NewReader(body))
	r.AddCookie(f.cookie)
	r.Header.Set("Origin", "https://evil.example.test")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF accepted")
	}
	f.enable(t, 3)
	current, _ := f.b.loadSchedule(context.Background(), f.p.HouseholdID)
	if w = f.request("PUT", "/api/household/backups/schedule", []byte(`{"enabled":true,"intervalHours":168,"keep":2}`), "", "enable-and-retain"); w.Code != 200 {
		t.Fatal("existing secret could not be retained", w.Code)
	}
	changed, _ := f.b.loadSchedule(context.Background(), f.p.HouseholdID)
	if changed.IntervalHours != 168 || !bytes.Equal(changed.sealed, current.sealed) || changed.next <= current.next {
		t.Fatal("frequency or password reuse incorrect")
	}
	other := f.p
	other.HouseholdID = "different-home"
	if _, err := f.b.openSchedulePassword(other, current.sealed); !errors.Is(err, errScheduleKey) {
		t.Fatal("wrapped secret not bound to household")
	}
	other = f.p
	other.MembershipID = "different-membership"
	if _, err := f.b.openSchedulePassword(other, current.sealed); !errors.Is(err, errScheduleKey) {
		t.Fatal("wrapped secret not bound to membership")
	}
	for _, role := range []string{"admin", "member", "guest"} {
		if _, err := f.db.Exec("UPDATE identity_memberships SET role=? WHERE user_id=?", role, f.p.UserID); err != nil {
			t.Fatal(err)
		}
		if w = f.request("GET", "/api/household/backups/history", nil, "", ""); w.Code != 403 {
			t.Fatal("nonowner accessed history", role, w.Code)
		}
		if w = f.request("PUT", "/api/household/backups/schedule", body, scheduleTestPassword, "enable-and-retain"); w.Code != 403 {
			t.Fatal("nonowner configured automation")
		}
		if w = f.request("POST", "/api/household/backups/schedule/run", nil, "", ""); w.Code != 403 {
			t.Fatal("nonowner ran automation")
		}
	}
}
func TestSchedulerRestartMissedRunIsolationAndExclusion(t *testing.T) {
	f := newScheduleFixture(t, true)
	f.enable(t, 2)
	f.b.mu.Lock()
	f.b.runDueSchedule(context.Background(), time.Now().Add(48*time.Hour))
	if w := f.request("POST", "/api/household/backups/schedule/run", nil, "", ""); w.Code != 423 {
		t.Fatal("parallel backup accepted", w.Code)
	}
	f.b.mu.Unlock()
	if len(f.history(t)) != 0 {
		t.Fatal("busy worker created history")
	}
	now := time.Now().UTC()
	if _, err := f.db.Exec("UPDATE household_backup_schedules SET next_run_at=?", now.Add(-72*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	// Startup notices persisted overdue work; no replay of all missed intervals.
	restarted, err := newBackups(context.Background(), f.db, f.b.accounts, f.b.dataDir, f.b.participants...)
	if err != nil {
		t.Fatal(err)
	}
	stop := restarted.startSchedules(context.Background())
	t.Cleanup(stop)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		f.db.QueryRow("SELECT count(*) FROM household_backup_history WHERE status='ready'").Scan(&count)
		if count == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	stop()
	h := f.history(t)
	if len(h) != 1 || h[0].Status != "ready" || h[0].Trigger != "scheduled" {
		t.Fatal("restart did not run overdue job", h)
	}
	s, _ := restarted.loadSchedule(context.Background(), f.p.HouseholdID)
	if s.next <= now.Unix() {
		t.Fatal("next run not advanced")
	}
	// A foreign home's rows and files cannot be downloaded or removed by this home's retention.
	foreignPath, _ := f.b.historyPath("neighbor", "foreign")
	os.MkdirAll(filepath.Dir(foreignPath), 0700)
	os.WriteFile(foreignPath, []byte("private"), 0600)
	if _, err = f.db.Exec("INSERT INTO household_backup_history(id,household_id,started_at,trigger,status,size_bytes) VALUES('foreign','neighbor',?,'scheduled','ready',7)", now.Unix()); err != nil {
		t.Fatal(err)
	}
	if w := f.request("GET", "/api/household/backups/history/foreign/download", nil, "", ""); w.Code != 404 {
		t.Fatal("foreign file leaked")
	}
	if len(f.history(t)) != 1 {
		t.Fatal("foreign metadata leaked")
	}
	if err = f.b.retainScheduled(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(foreignPath); string(data) != "private" {
		t.Fatal("foreign file pruned")
	}
	partialPath, _ := f.b.historyPath(f.p.HouseholdID, "partial")
	os.WriteFile(partialPath, []byte("incomplete"), 0600)
	if _, err = f.db.Exec("INSERT INTO household_backup_history(id,household_id,started_at,trigger,status) VALUES('partial',?,?,'scheduled','running')", f.p.HouseholdID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = newBackups(context.Background(), f.db, f.b.accounts, f.b.dataDir, f.b.participants...); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(partialPath); !os.IsNotExist(err) {
		t.Fatal("interrupted published file left downloadable")
	}
	h = f.history(t)
	if h[0].Status != "failed" || h[0].ErrorCode != "backup_interrupted" || !h[1].Available {
		t.Fatal("interrupted history incorrect", h)
	}
}

func TestScheduleRetentionFailureHistoryBoundsAndMissingFiles(t *testing.T) {
	f := newScheduleFixture(t, true)
	f.enable(t, 1)
	f.due(t, time.Now().UTC())
	first := f.history(t)[0]
	path, _ := f.b.historyPath(f.p.HouseholdID, first.ID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "sentinel"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	f.due(t, time.Now().UTC())
	h := f.history(t)
	if len(h) != 2 || h[0].Status != "ready" || !h[0].Available || h[0].ErrorCode != "backup_retention_failed" || h[1].Available {
		t.Fatal("retention failure misreported", h)
	}
	if w := f.request("GET", "/api/household/backups/history/"+first.ID+"/download", nil, "", ""); w.Code != 404 {
		t.Fatal("non-file downloaded")
	}
	os.Remove(filepath.Join(path, "sentinel"))
	os.Remove(path)
	os.WriteFile(path, original, 0600)
	s, _ := f.b.loadSchedule(context.Background(), f.p.HouseholdID)
	if err = f.b.retainScheduled(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("retention did not retry")
	}
	for i := 0; i < 210; i++ {
		if _, err = f.db.Exec("INSERT INTO household_backup_history(id,household_id,started_at,trigger,status) VALUES(?,?,1,'scheduled','failed')", fmt.Sprintf("failed-%d", i), f.p.HouseholdID); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.b.trimHistory(context.Background(), f.p.HouseholdID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.db.QueryRow("SELECT count(*) FROM household_backup_history WHERE household_id=?", f.p.HouseholdID).Scan(&count); err != nil || count != 201 {
		t.Fatal("unbounded history or retained record trimmed", count, err)
	}
	w := f.request("GET", "/api/household/backups/history?offset=200", nil, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatal("retained backup inaccessible after many failures", w.Code, w.Body.String())
	}
	if w = f.request("GET", "/api/household/backups/history?offset=-1", nil, "", ""); w.Code != 400 {
		t.Fatal("invalid pagination accepted")
	}
}
func TestScheduleSurvivesDatabaseReopen(t *testing.T) {
	f := newScheduleFixture(t, true)
	f.enable(t, 2)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(filepath.Join(f.b.dataDir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	application, err := New(context.Background(), reopened, fstest.MapFS{"index.html": {Data: []byte("HomeAtlas")}}, scheduleTestOrigin, f.b.dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	f.handler = application.Handler
	f.db = reopened
	w := f.request("GET", "/api/household/backups/schedule", nil, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"passwordConfigured":true`) {
		t.Fatal("schedule not persisted", w.Code, w.Body.String())
	}
	w = f.request("POST", "/api/household/backups/schedule/run", nil, "", "")
	if w.Code != 201 || !f.history(t)[0].Available {
		t.Fatal("saved password could not run after restart", w.Code, w.Body.String())
	}
}

func TestScheduleKeyRequiresPrivateIntactMaterialAndFreshNonces(t *testing.T) {
	b := &householdBackups{dataDir: t.TempDir()}
	p := access.Principal{HouseholdID: "home", UserID: "owner", MembershipID: "membership"}
	sealed, err := b.sealSchedulePassword(p, scheduleTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.sealSchedulePassword(p, scheduleTestPassword)
	if err != nil || bytes.Equal(sealed, second) {
		t.Fatal("nonce reused", err)
	}
	if plain, err := b.openSchedulePassword(p, sealed); err != nil || plain != scheduleTestPassword {
		t.Fatal("secret roundtrip failed", err)
	}
	damaged := bytes.Clone(sealed)
	damaged[len(damaged)-1] ^= 1
	if _, err = b.openSchedulePassword(p, damaged); !errors.Is(err, errScheduleKey) {
		t.Fatal("damaged wrapping accepted")
	}
	path := filepath.Join(b.dataDir, "backup-schedule.key")
	key, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = b.openSchedulePassword(p, sealed); !errors.Is(err, errScheduleKey) {
		t.Fatal("insecure key file accepted")
	}
	if _, err = b.sealSchedulePassword(p, scheduleTestPassword); !errors.Is(err, errScheduleKey) {
		t.Fatal("insecure existing key overwritten")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, key) {
		t.Fatal("existing key changed")
	}
	os.Chmod(path, 0600)
	os.WriteFile(path, key[:31], 0600)
	if _, err = b.sealSchedulePassword(p, scheduleTestPassword); !errors.Is(err, errScheduleKey) {
		t.Fatal("corrupt key silently replaced")
	}
	if after, _ := os.ReadFile(path); len(after) != 31 {
		t.Fatal("corrupt key overwritten")
	}
	os.Remove(path)
	if _, err = b.openSchedulePassword(p, sealed); !errors.Is(err, errScheduleKey) {
		t.Fatal("lost key accepted")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("lost key regenerated while reading")
	}
}
