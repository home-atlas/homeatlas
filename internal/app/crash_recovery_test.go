//go:build !race && unix

package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"filippo.io/age"
	"github.com/home-atlas/homeatlas/internal/core/backup"
	"github.com/home-atlas/homeatlas/internal/core/collections"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
	"github.com/home-atlas/homeatlas/internal/core/modules"
	"github.com/home-atlas/homeatlas/internal/core/objectstore"
	"github.com/home-atlas/homeatlas/internal/core/plugins"
	"github.com/home-atlas/homeatlas/internal/core/recovery"
)

const crashPassword = "Crash recovery private fixture 2026"

var crashAssets = fstest.MapFS{"index.html": {Data: []byte("<html>HomeAtlas</html>")}}

// Only test wrappers stop execution. Production has no fault-injection switches.
func crashCheckpoint(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(os.Getenv("HOMEATLAS_CRASH_MARKER"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

type crashProvider struct {
	backup.Provider
	phase string
	t     *testing.T
}

func (p crashProvider) Export(ctx context.Context, q backup.Queryer, out *sql.Tx, home string, degraded bool) error {
	if err := p.Provider.Export(ctx, q, out, home, degraded); err != nil {
		return err
	}
	if p.phase == "backup-snapshot" {
		crashCheckpoint(p.t)
	}
	return nil
}
func (p crashProvider) Prepare(ctx context.Context, q backup.Queryer, dir, home string) (backup.Plan, error) {
	plan, err := p.Provider.Prepare(ctx, q, dir, home)
	if err != nil {
		return nil, err
	}
	if p.phase == "restore-staged" {
		crashCheckpoint(p.t)
	}
	return crashPlan{plan, p}, nil
}

type crashPlan struct {
	backup.Plan
	provider crashProvider
}

func (p crashPlan) Apply(ctx context.Context, tx *sql.Tx, rc backup.RestoreContext) error {
	if err := p.Plan.Apply(ctx, tx, rc); err != nil {
		return err
	}
	if p.provider.phase == "restore-transaction" {
		crashCheckpoint(p.provider.t)
	}
	return nil
}
func (p crashPlan) Finish(committed, keepOld bool) {
	if committed && strings.HasPrefix(p.provider.phase, "restore-committed") {
		crashCheckpoint(p.provider.t)
	}
	p.Plan.Finish(committed, keepOld)
}

type crashContext struct {
	context.Context
	dir, phase string
	t          *testing.T
}

func (c crashContext) Err() error {
	pattern := ""
	if c.phase == "backup-encryption" {
		pattern = "backup.zip.age"
	}
	if c.phase == "restore-decryption" {
		pattern = "decrypted.zip"
	}
	if pattern != "" {
		matches, _ := filepath.Glob(filepath.Join(c.dir, ".household-backup-*", pattern))
		if len(matches) > 0 {
			crashCheckpoint(c.t)
		}
	}
	return c.Context.Err()
}

type crashReader struct {
	io.Reader
	t *testing.T
}

func (r crashReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		crashCheckpoint(r.t)
	}
	return n, err
}

func crashRequest(handler http.Handler, method, path string, body []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, scheduleTestOrigin+path, bytes.NewReader(body))
	r.Header.Set("Origin", scheduleTestOrigin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(crashPassword)))
	if path == "/api/household/backups/schedule" {
		r.Header.Set("X-HomeAtlas-Confirm-Schedule", "enable-and-retain")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// The child uses the real collection use cases, SQLite snapshot plans, crypto
// and coordinator. Wrappers only expose the exact pre/post-commit checkpoints.
func TestCrashRecoveryChild(t *testing.T) {
	phase := os.Getenv("HOMEATLAS_CRASH_PHASE")
	if phase == "" {
		return
	}
	dir := os.Getenv("HOMEATLAS_CRASH_DATA")
	ctx := context.Background()
	lock, err := recovery.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages, err := plugins.Load(filepath.Join(dir, "plugins"))
	if err != nil || len(packages) != 1 {
		t.Fatal(err)
	}
	pkg := packages[0]
	registry, err := modules.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := identity.New(ctx, db, identity.Config{PublicURL: scheduleTestOrigin, SetupCodePath: filepath.Join(dir, "setup-code"), Modules: registry, OwnerCapabilities: append([]string{identity.Manage, backupPermission}, pkg.Capabilities()...), DelegableCapabilities: pkg.Capabilities()})
	if err != nil {
		t.Fatal(err)
	}
	defer accounts.Close()
	engine := &plugins.Engine{}
	defer engine.Close()
	storage, err := objectstore.New(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	config := plugins.CollectionConfig(pkg, engine)
	config.Storage = collections.RemoteStorage(storage)
	docs, err := collections.New(db, plugins.CollectionDirectory(dir, pkg), accounts, config)
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	if err = registry.Register(ctx, docs); err != nil {
		t.Fatal(err)
	}
	provider := plugins.CollectionBackup{Provider: collections.NewBackup(filepath.Dir(plugins.CollectionDirectory(dir, pkg)), accounts, config), Package: pkg}
	b, err := newBackups(ctx, db, accounts, dir, backup.Participant{Provider: crashProvider{provider, phase, t}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	accounts.Mount(mux)
	registry.Mount(mux)
	b.mount(mux)
	handler := securityHeaders(accounts.Middleware(b.guard(mux)))
	cookie := &http.Cookie{Name: os.Getenv("HOMEATLAS_CRASH_COOKIE_NAME"), Value: os.Getenv("HOMEATLAS_CRASH_COOKIE")}
	var body io.Reader
	path := "/api/household/backups"
	contentType := "application/zip"
	switch phase {
	case "upload":
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		if err := writer.WriteField("metadata", `{"title":"Uncommitted upload","category":"otros","description":"","tags":[]}`); err != nil {
			t.Fatal(err)
		}
		part, err := writer.CreateFormFile("file", "interrupted.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(bytes.Repeat([]byte("partial original\n"), 4096)); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		body = crashReader{bytes.NewReader(payload.Bytes()[:payload.Len()-512]), t}
		path = "/api/documents"
		contentType = writer.FormDataContentType()
	case "restore-staged", "restore-transaction", "restore-committed", "restore-committed-damaged", "restore-decryption":
		raw, err := os.ReadFile(os.Getenv("HOMEATLAS_CRASH_ARCHIVE"))
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
		path = "/api/household/restore"
	case "scheduled-published":
		home := os.Getenv("HOMEATLAS_CRASH_HOME")
		p, err := accounts.Authenticate(ctx, cookie.Value)
		if err != nil {
			t.Fatal(err)
		}
		// Same publication boundary as runScheduled: durable running row, file
		// fsynced/renamed, then outcome commit. The process dies between the last two.
		s, err := b.loadSchedule(p, home)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO household_backup_history(id,household_id,started_at,trigger,status) VALUES('interrupted',?,?,'scheduled','running')", home, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		principal, err := b.authorize(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.writeScheduledBackup(p, s, principal, "interrupted"); err != nil {
			t.Fatal(err)
		}
		crashCheckpoint(t)
	}
	r := httptest.NewRequest("POST", scheduleTestOrigin+path, body)
	r = r.WithContext(crashContext{ctx, dir, phase, t})
	r.AddCookie(cookie)
	r.Header.Set("Origin", scheduleTestOrigin)
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("X-HomeAtlas-Backup-Version", "4")
	r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(crashPassword)))
	r.Header.Set("X-HomeAtlas-Confirm-Restore", "replace-household")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	t.Fatalf("checkpoint not reached: %s returned %d %s", phase, w.Code, w.Body.String())
}

func TestPersistentPluginCrashRecovery(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	// Build and approve the real public Documents guest once for all checkpoints.
	wasm := filepath.Join(t.TempDir(), "documents.wasm")
	hap := filepath.Join(t.TempDir(), "documents.hap")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", wasm, "./plugins/documents")
	build.Dir = "../.."
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0", "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v %s", err, out)
	}
	pack := exec.Command("python3", "scripts/package-plugin.py", "plugins/documents/manifest.json", wasm, hap)
	pack.Dir = "../.."
	if out, err := pack.CombinedOutput(); err != nil {
		t.Fatalf("package guest: %v %s", err, out)
	}
	raw, err := os.ReadFile(hap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plugins.Install(ctx, filepath.Join(base, "plugins"), hap, fmt.Sprintf("%x", sha256.Sum256(raw)), []string{"documents.read", "documents.write", "documents.share", "documents.files", "documents.collection"}); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(base, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewWithOptions(ctx, db, crashAssets, scheduleTestOrigin, base, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close(); db.Close() }()
	code, err := os.ReadFile(filepath.Join(base, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	setup, _ := json.Marshal(map[string]string{"code": strings.TrimSpace(string(code)), "name": "Crash fixture", "username": "crash-owner", "password": "Private account fixture 2026", "householdName": "Crash household", "timezone": "UTC"})
	w := crashRequest(a.Handler, "POST", "/api/setup", setup, nil)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	var state identity.State
	if err = json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	home, owner := state.Session.Household.ID, state.Session.User.ID
	content := []byte("Committed original survives interrupted operations\n")
	key := "committed-original"
	if err = os.WriteFile(filepath.Join(base, "documents", key), content, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO documents(id,household_id,owner_id,title,category,original_filename,media_type,size_bytes,sha256,file_key) VALUES('original',?,?,'Snapshot title','otros','original.txt','text/plain',?,?,?)`, home, owner, len(content), fmt.Sprintf("%x", sha256.Sum256(content)), key); err != nil {
		t.Fatal(err)
	}
	// Keep a second household and a revoked capability across every interruption.
	if _, err = db.Exec(`INSERT INTO documents(id,household_id,owner_id,title,category,original_filename,media_type,size_bytes,sha256,file_key) VALUES('foreign','another-home',?,'Foreign','otros','foreign.txt','text/plain',?,?, 'foreign-key')`, owner, len(content), fmt.Sprintf("%x", sha256.Sum256(content))); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(base, "documents", "foreign-key"), content, 0600); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", scheduleTestOrigin+"/api/household/backups", nil)
	r.AddCookie(cookie)
	r.Header.Set("Origin", scheduleTestOrigin)
	r.Header.Set("X-HomeAtlas-Backup-Version", "4")
	r.Header.Set("X-HomeAtlas-Backup-Password", base64.StdEncoding.EncodeToString([]byte(crashPassword)))
	w = httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	archive := append([]byte(nil), w.Body.Bytes()...)
	archivePath := filepath.Join(t.TempDir(), "source.zip.age")
	if err = os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE documents SET title='Live title' WHERE id='original'; DELETE FROM identity_permissions WHERE user_id=? AND capability='documents.share'", owner); err != nil {
		t.Fatal(err)
	}
	schedule, _ := json.Marshal(map[string]any{"enabled": true, "intervalHours": 24, "keep": 3})
	w = crashRequest(a.Handler, "PUT", "/api/household/backups/schedule", schedule, cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	goodPath := filepath.Join(base, "backups", "scheduled", home, "good.zip.age")
	if err = os.MkdirAll(filepath.Dir(goodPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(goodPath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO household_backup_history(id,household_id,started_at,finished_at,trigger,status,size_bytes) VALUES('good',?,?,?,'scheduled','ready',?)", home, time.Now().Unix(), time.Now().Unix(), len(archive)); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"upload", "backup-snapshot", "backup-encryption", "scheduled-published", "restore-decryption", "restore-staged", "restore-transaction", "restore-committed", "restore-committed-damaged"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.CopyFS(dir, os.DirFS(base)); err != nil {
				t.Fatal(err)
			}
			// CopyFS copies contents, but private wrapping keys require exact modes.
			if err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(base, path)
				if err != nil {
					return err
				}
				return os.Chmod(filepath.Join(dir, rel), info.Mode().Perm())
			}); err != nil {
				t.Fatal(err)
			}
			if phase == "restore-committed-damaged" {
				if err := os.WriteFile(filepath.Join(dir, "documents", key), []byte("damaged original retained for inspection"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(t.TempDir(), "checkpoint")
			command := exec.Command(os.Args[0], "-test.run=^TestCrashRecoveryChild$", "-test.v")
			command.Env = append(os.Environ(), "HOMEATLAS_CRASH_PHASE="+phase, "HOMEATLAS_CRASH_DATA="+dir, "HOMEATLAS_CRASH_MARKER="+marker, "HOMEATLAS_CRASH_COOKIE="+cookie.Value, "HOMEATLAS_CRASH_COOKIE_NAME="+cookie.Name, "HOMEATLAS_CRASH_HOME="+home, "HOMEATLAS_CRASH_ARCHIVE="+archivePath)
			log, err := os.Create(filepath.Join(t.TempDir(), "child.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command.Stdout = log
			command.Stderr = log
			if err = command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			defer command.Process.Kill()
			deadline := time.Now().Add(45 * time.Second)
			for {
				if _, err = os.Stat(marker); err == nil {
					break
				}
				select {
				case err := <-done:
					raw, _ := os.ReadFile(log.Name())
					t.Fatalf("child exited before checkpoint: %v\n%s", err, raw)
				default:
				}
				if time.Now().After(deadline) {
					command.Process.Kill()
					<-done
					raw, _ := os.ReadFile(log.Name())
					t.Fatalf("checkpoint timeout\n%s", raw)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err = command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err == nil {
				t.Fatal("child was not killed")
			}
			staged, _ := filepath.Glob(filepath.Join(dir, ".household-backup-*"))
			if phase != "upload" && phase != "scheduled-published" && len(staged) == 0 {
				t.Fatal("interrupted workspace not reproduced")
			}
			reopened, err := database.Open(filepath.Join(dir, "homeatlas.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			options := Options{}
			if phase == "restore-staged" {
				options.DisableExternalPlugins = true
			}
			restarted, err := NewWithOptions(ctx, reopened, crashAssets, scheduleTestOrigin, dir, nil, options)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			var check string
			if err = reopened.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
				t.Fatal("database integrity", check, err)
			}
			rows, err := reopened.Query("PRAGMA foreign_key_check")
			if err != nil {
				t.Fatal(err)
			}
			invalid := rows.Next()
			rows.Close()
			if invalid {
				t.Fatal("foreign key damage")
			}
			var title, liveKey string
			if err = reopened.QueryRow("SELECT title,file_key FROM documents WHERE id='original'").Scan(&title, &liveKey); err != nil {
				t.Fatal(err)
			}
			want := "Live title"
			if strings.HasPrefix(phase, "restore-committed") {
				want = "Snapshot title"
			}
			if title != want {
				t.Fatal("partial transaction", title, want)
			}
			got, err := os.ReadFile(filepath.Join(dir, "documents", liveKey))
			if err != nil || !bytes.Equal(got, content) {
				t.Fatal("committed original lost", err)
			}
			got, err = os.ReadFile(filepath.Join(dir, "documents", "foreign-key"))
			if err != nil || !bytes.Equal(got, content) {
				t.Fatal("another household changed", err)
			}
			var share int
			if err = reopened.QueryRow("SELECT count(*) FROM identity_permissions WHERE user_id=? AND capability='documents.share'", owner).Scan(&share); err != nil || share != 0 {
				t.Fatal("revoked capability restored", share, err)
			}
			staged, _ = filepath.Glob(filepath.Join(dir, ".household-backup-*"))
			if len(staged) > 0 {
				t.Fatal("plaintext workspace retained", staged)
			}
			entries, err := os.ReadDir(filepath.Join(dir, "documents"))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != liveKey && entry.Name() != "foreign-key" && entry.Name() != ".recovered" {
					t.Fatal("unreferenced original not reconciled", entry.Name())
				}
			}
			if phase == "upload" || phase == "restore-staged" || phase == "restore-transaction" || strings.HasPrefix(phase, "restore-committed") {
				preserved, _ := filepath.Glob(filepath.Join(dir, "documents", ".recovered", "*", "*"))
				if len(preserved) == 0 {
					t.Fatal("interrupted original was not preserved")
				}
				if phase == "restore-committed-damaged" {
					damaged, _ := filepath.Glob(filepath.Join(dir, "documents", ".recovered", "*", key))
					if len(damaged) != 1 {
						t.Fatal("degraded original lost", damaged)
					}
					raw, err := os.ReadFile(damaged[0])
					if err != nil || string(raw) != "damaged original retained for inspection" {
						t.Fatal("degraded original overwritten", err)
					}
				}
			}
			good, err := os.ReadFile(filepath.Join(dir, "backups", "scheduled", home, "good.zip.age"))
			if err != nil || !bytes.Equal(good, archive) {
				t.Fatal("last good backup lost", err)
			}
			if phase == "scheduled-published" {
				var status, code string
				if err = reopened.QueryRow("SELECT status,error_code FROM household_backup_history WHERE id='interrupted'").Scan(&status, &code); err != nil || status != "failed" || code != "backup_interrupted" {
					t.Fatal("interrupted history not recovered", status, code, err)
				}
				if _, err = os.Stat(filepath.Join(dir, "backups", "scheduled", home, "interrupted.zip.age")); !os.IsNotExist(err) {
					t.Fatal("incomplete published backup retained", err)
				}
			}
			if w := crashRequest(restarted.Handler, "GET", "/api/household/settings", nil, nil); w.Code != 401 {
				t.Fatal("anonymous access after restart", w.Code)
			}
			if phase != "restore-staged" {
				w := crashRequest(restarted.Handler, "GET", "/api/documents/original/download", nil, cookie)
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), content) {
					t.Fatal("authenticated download after restart", w.Code, w.Body.String())
				}
			}
			t.Logf("SIGKILL %s: SQLite/originals/permissions/last backup intact; temporary files reconciled", phase)
		})
	}
	// Authenticate the complete surviving archive, not just its age header.
	keyIdentity, err := age.NewScryptIdentity(crashPassword)
	if err != nil {
		t.Fatal(err)
	}
	keyIdentity.SetMaxWorkFactor(18)
	decrypted, err := age.Decrypt(bytes.NewReader(archive), keyIdentity)
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := io.ReadAll(decrypted)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range reader.File {
		f, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, f)
		f.Close()
		if err != nil {
			t.Fatal("backup CRC", err)
		}
	}
}
