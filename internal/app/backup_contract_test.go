package app

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/backup"
)

// This provider exists only in tests: it proves a second domain needs no
// coordinator branches, SQL knowledge, filename handling or count aliases.
type notesBackup struct {
	failPrepare, failApply bool
	finishes               []bool
}
type note struct{ id, value string }

func (*notesBackup) ID() string          { return "notes" }
func (*notesBackup) Version() int        { return 1 }
func (*notesBackup) MaxFileBytes() int64 { return 1024 }
func (*notesBackup) Tables() []string {
	return []string{`CREATE TABLE contract_notes (id TEXT PRIMARY KEY, value TEXT NOT NULL)`}
}
func (*notesBackup) Present(ctx context.Context, q backup.Queryer) (bool, error) {
	var yes bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='contract_notes_live')").Scan(&yes)
	return yes, err
}
func (*notesBackup) Empty(ctx context.Context, q backup.Queryer) (bool, error) {
	var yes bool
	err := q.QueryRowContext(ctx, "SELECT NOT EXISTS(SELECT 1 FROM contract_notes_live)").Scan(&yes)
	return yes, err
}
func (*notesBackup) Export(ctx context.Context, q backup.Queryer, out *sql.Tx, household string, _ bool) error {
	rows, err := q.QueryContext(ctx, "SELECT id,value FROM contract_notes_live WHERE household_id=?", household)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.id, &n.value); err != nil {
			return err
		}
		if _, err := out.ExecContext(ctx, "INSERT INTO contract_notes VALUES(?,?)", n.id, n.value); err != nil {
			return err
		}
	}
	return rows.Err()
}
func readNotes(ctx context.Context, q backup.Queryer) ([]note, error) {
	rows, err := q.QueryContext(ctx, "SELECT id,value FROM contract_notes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := []note{}
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.id, &n.value); err != nil {
			return nil, err
		}
		if !backupKey.MatchString(n.id) || !backupText(n.value, 160) {
			return nil, backup.Invalid
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
func (*notesBackup) Inspect(ctx context.Context, q backup.Queryer, _ string) (backup.Contents, error) {
	notes, err := readNotes(ctx, q)
	return backup.Contents{Records: len(notes)}, err
}
func (*notesBackup) OpenFile(string) (*os.File, error) { return nil, os.ErrNotExist }
func (p *notesBackup) Prepare(ctx context.Context, q backup.Queryer, _, _ string) (backup.Plan, error) {
	if p.failPrepare {
		return nil, errors.New("forced prepare failure")
	}
	notes, err := readNotes(ctx, q)
	return &notesPlan{p, notes}, err
}

type notesPlan struct {
	provider *notesBackup
	notes    []note
}

func (p *notesPlan) Apply(ctx context.Context, tx *sql.Tx, rc backup.RestoreContext) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM contract_notes_live WHERE household_id=?", rc.HouseholdID); err != nil {
		return err
	}
	for _, n := range p.notes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO contract_notes_live VALUES(?,?,?)", n.id, rc.HouseholdID, n.value); err != nil {
			return err
		}
	}
	if p.provider.failApply {
		return errors.New("forced apply failure")
	}
	return nil
}
func (p *notesPlan) Finish(committed, _ bool) {
	p.provider.finishes = append(p.provider.finishes, committed)
}
func addNotes(t *testing.T, f scheduleFixture, withData bool) *notesBackup {
	t.Helper()
	if _, err := f.db.Exec(`CREATE TABLE contract_notes_live (id TEXT NOT NULL, household_id TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY(id,household_id)); INSERT INTO module_states VALUES('notes',1)`); err != nil {
		t.Fatal(err)
	}
	if withData {
		if _, err := f.db.Exec("INSERT INTO contract_notes_live VALUES('note',?,'before')", f.p.HouseholdID); err != nil {
			t.Fatal(err)
		}
	}
	p := &notesBackup{}
	f.b.participants = append(f.b.participants, backup.Participant{Provider: p})
	return p
}
func createContractArchive(t *testing.T, f scheduleFixture, version int) (string, householdSnapshot, string) {
	t.Helper()
	ctx := access.WithPrincipal(context.Background(), f.p)
	dir := t.TempDir()
	path, _, err := f.b.create(ctx, dir, false, version)
	if err != nil {
		t.Fatal(err)
	}
	validated := filepath.Join(dir, "validated")
	s, err := f.b.extractBackup(ctx, path, validated, f.p.HouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	return path, s, validated
}
func documentFiles(t *testing.T, f scheduleFixture) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.b.dataDir, "documents"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
func assertNotesAndTitle(t *testing.T, f scheduleFixture, noteValue, title string) {
	t.Helper()
	var value, gotTitle string
	if err := f.db.QueryRow("SELECT value FROM contract_notes_live WHERE household_id=?", f.p.HouseholdID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow("SELECT title FROM documents WHERE id='document'").Scan(&gotTitle); err != nil {
		t.Fatal(err)
	}
	if value != noteValue || gotTitle != title {
		t.Fatal("module state changed unexpectedly", value, gotTitle)
	}
}
func TestBackupContractAtomicRestoreAcrossModules(t *testing.T) {
	f := newScheduleFixture(t, true)
	notes := addNotes(t, f, true)
	_, s, dir := createContractArchive(t, f, 4)
	if len(s.parts) != 2 || s.version != 4 {
		t.Fatal("second module omitted", s.parts, s.version)
	}
	if _, err := f.db.Exec("UPDATE documents SET title='after'; UPDATE contract_notes_live SET value='after'"); err != nil {
		t.Fatal(err)
	}
	before := documentFiles(t, f)
	ctx := access.WithPrincipal(context.Background(), f.p)
	for _, stage := range []string{"prepare", "apply"} {
		notes.failPrepare = stage == "prepare"
		notes.failApply = stage == "apply"
		if err := f.b.apply(ctx, s, dir, false, nil); err == nil {
			t.Fatal("forced failure succeeded", stage)
		}
		assertNotesAndTitle(t, f, "after", "after")
		if !slices.Equal(before, documentFiles(t, f)) {
			t.Fatal("failed restore leaked or removed originals", stage)
		}
	}
	notes.failPrepare = false
	notes.failApply = false
	if err := f.b.apply(ctx, s, dir, false, nil); err != nil {
		t.Fatal(err)
	}
	assertNotesAndTitle(t, f, "before", "Manual privado")
	if !slices.Equal(notes.finishes, []bool{false, true}) {
		t.Fatal("transaction outcome not delivered to provider", notes.finishes)
	}
	var key string
	if err := f.db.QueryRow("SELECT file_key FROM documents WHERE id='document'").Scan(&key); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(f.b.dataDir, "documents", key))
	if err != nil || string(content) != "Original privado del hogar de prueba\n" {
		t.Fatal("restored original differs", err)
	}
}
func TestBackupContractLegacyRestorePreservesLaterModule(t *testing.T) {
	f := newScheduleFixture(t, true)
	_, legacy, dir := createContractArchive(t, f, 2)
	addNotes(t, f, true)
	if _, err := f.db.Exec("UPDATE documents SET title='after'; INSERT INTO household_module_states VALUES(?,'notes',0)", f.p.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if err := f.b.apply(access.WithPrincipal(context.Background(), f.p), legacy, dir, false, nil); err != nil {
		t.Fatal(err)
	}
	assertNotesAndTitle(t, f, "before", "Manual privado")
	var enabled bool
	if err := f.db.QueryRow("SELECT enabled FROM household_module_states WHERE household_id=? AND module_id='notes'", f.p.HouseholdID).Scan(&enabled); err != nil || enabled {
		t.Fatal("unrelated module preference lost", err)
	}
}
func TestBackupContractMissingOrIncompatibleProviderFailsClosed(t *testing.T) {
	f := newScheduleFixture(t, true)
	addNotes(t, f, true)
	path, s, _ := createContractArchive(t, f, 2)
	if s.version != 4 {
		t.Fatal("legacy request silently omitted new module")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	altered := rewriteContractManifest(t, raw, func(m *backupManifest) { m.Modules[1].Version = 2 })
	invalidPath := filepath.Join(t.TempDir(), "incompatible.zip")
	if err := os.WriteFile(invalidPath, altered, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.b.extractBackup(context.Background(), invalidPath, filepath.Join(t.TempDir(), "validated"), f.p.HouseholdID); !errors.Is(err, backup.ModuleRequired) {
		t.Fatal("incompatible provider accepted", err)
	}
	f.b.participants = f.b.participants[:1]
	if _, err := f.b.extractBackup(context.Background(), path, filepath.Join(t.TempDir(), "validated"), f.p.HouseholdID); !errors.Is(err, backup.ModuleRequired) {
		t.Fatal("unknown module skipped on import", err)
	}
	if _, _, err := f.b.create(access.WithPrincipal(context.Background(), f.p), t.TempDir(), false, 4); !errors.Is(err, backup.ModuleRequired) {
		t.Fatal("unknown stored module omitted from export", err)
	}
	assertNotesAndTitle(t, f, "before", "Manual privado")
}
func TestBackupContractNewInstallationRecoversEveryModule(t *testing.T) {
	source := newScheduleFixture(t, true)
	addNotes(t, source, true)
	path, _, _ := createContractArchive(t, source, 4)
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	target := newScheduleFixture(t, false)
	addNotes(t, target, false)
	r := httptest.NewRequest("POST", scheduleTestOrigin+"/api/setup/restore", bytes.NewReader(archive))
	r.Header.Set("Origin", scheduleTestOrigin)
	r.Header.Set("Content-Type", "application/zip")
	r.Header.Set("X-HomeAtlas-Allow-Legacy-Backup", "true")
	r.Header.Set("X-HomeAtlas-Setup-Code", target.code)
	r.Header.Set("X-HomeAtlas-Confirm-Restore", "initialize-installation")
	w := httptest.NewRecorder()
	target.handler.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	target.p = source.p
	assertNotesAndTitle(t, target, "before", "Manual privado")
	var report struct{ ModuleRecords map[string]int }
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil || report.ModuleRecords["notes"] != 1 || report.ModuleRecords["documents"] != 1 {
		t.Fatal("module report missing", w.Body.String(), err)
	}
}
func TestBackupContractV4RejectsInvalidEnvelope(t *testing.T) {
	f := newScheduleFixture(t, true)
	addNotes(t, f, true)
	path, _, _ := createContractArchive(t, f, 4)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*backupManifest){
		"duplicate module":         func(m *backupManifest) { m.Modules = append(m.Modules, m.Modules[0]) },
		"inconsistent core marker": func(m *backupManifest) { m.CoreOnly = true },
		"corrupt module file": func(m *backupManifest) {
			for path := range m.SHA256 {
				if path != "household.sqlite" {
					m.SHA256[path] = "invalid"
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "invalid.zip")
			if err := os.WriteFile(path, rewriteContractManifest(t, raw, mutate), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.b.extractBackup(context.Background(), path, filepath.Join(dir, "validated"), f.p.HouseholdID); !errors.Is(err, backup.Invalid) {
				t.Fatal("invalid envelope accepted", err)
			}
			assertNotesAndTitle(t, f, "before", "Manual privado")
		})
	}
}
func rewriteContractManifest(t *testing.T, data []byte, mutate func(*backupManifest)) []byte {
	t.Helper()
	source, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	out := zip.NewWriter(&buf)
	for _, entry := range source.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "manifest.json" {
			var m backupManifest
			if err := json.Unmarshal(content, &m); err != nil {
				t.Fatal(err)
			}
			mutate(&m)
			content, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
		}
		w, err := out.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
