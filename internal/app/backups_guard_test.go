package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
)

func newBackupGuard() *householdBackups {
	return &householdBackups{resources: semaphore.NewWeighted(backupLockCapacity), mutations: semaphore.NewWeighted(1)}
}

func TestCancelledRequestsLeaveBackupQueues(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "mutation", true: "restore"}[restore], func(t *testing.T) {
			b := newBackupGuard()
			lock, permits := b.mutations, int64(1)
			if restore {
				lock, permits = b.resources, backupLockCapacity
			}
			if !lock.TryAcquire(permits) {
				t.Fatal("could not hold gate")
			}
			defer lock.Release(permits)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, done := make(chan struct{}), make(chan struct{})
			handler := b.guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("cancelled request reached handler") }))
			go func() {
				close(started)
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/mutation", nil).WithContext(ctx))
				close(done)
			}()
			<-started
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancelled request remained queued")
			}
		})
	}
}

func TestJSONAdmissionPreservesBodyLimits(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	r := httptest.NewRequest("POST", "/api/ext-test/actions/write", reader)
	r.Header.Set("Content-Type", "application/json")
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		newBackupGuard().guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10)); err == nil {
				t.Error("oversized body accepted")
			}
			w.WriteHeader(400)
		})).ServeHTTP(w, r)
		done <- w.Code
	}()
	if _, err := writer.Write([]byte(strings.Repeat(" ", (16<<10)+1))); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 400 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("oversized JSON waited for more network input")
	}
}

func TestSlowJSONBodyDoesNotFreezeOtherMutations(t *testing.T) {
	b := newBackupGuard()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	handler := b.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			_, _ = io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		}
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("POST", "/api/auth/login", reader)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	slowDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), r)
		close(slowDone)
	}()
	defer func() { writer.Close(); <-slowDone }()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/auth/logout", nil))
		done <- w.Code
	}()
	select {
	case status := <-done:
		if status != 204 {
			t.Fatal(status)
		}
	case <-time.After(time.Second):
		t.Fatal("incomplete anonymous JSON body froze another mutation")
	}
}

func TestBackupExportDoesNotQueueDeclaredPluginQueries(t *testing.T) {
	b := newBackupGuard()
	b.readOnlyActions = map[string]bool{"POST /api/ext-test/actions/read": true}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	handler := b.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/household/backups" {
			close(started)
			<-release
		}
		w.WriteHeader(200)
	}))
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/household/backups", nil))
		close(done)
	}()
	<-started
	read := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/ext-test/actions/read", nil))
		read <- w.Code
	}()
	select {
	case status := <-read:
		if status != 200 {
			t.Fatal(status)
		}
	case <-time.After(time.Second):
		t.Fatal("declared read queued behind backup")
	}
	finish()
	<-done
}

func TestBackupExportAllowsReadersButFreezesMutations(t *testing.T) {
	for _, path := range []string{"/api/household/backups", "/api/household/backups/schedule/run"} {
		t.Run(path, func(t *testing.T) {
			b := newBackupGuard()
			started, release, exported, mutated := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			t.Cleanup(finish)
			handler := b.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case path:
					close(started)
					<-release
				case "/mutation":
					close(mutated)
				}
				w.WriteHeader(200)
			}))
			go func() {
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", path, nil))
				close(exported)
			}()
			<-started
			go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/mutation", nil))
			read := make(chan struct{})
			go func() {
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/documents", nil))
				close(read)
			}()
			select {
			case <-read:
			case <-time.After(2 * time.Second):
				t.Fatal("export blocked a reader")
			}
			select {
			case <-mutated:
				t.Fatal("mutation ran before the export finished")
			default:
			}
			for _, busy := range []string{path, "/api/household/restore", "/api/setup/restore"} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest("POST", busy, nil))
				if w.Code != 423 {
					t.Fatalf("parallel operation %s: %d", busy, w.Code)
				}
			}
			finish()
			for _, done := range []chan struct{}{exported, mutated} {
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("operation did not resume after export")
				}
			}
		})
	}
}

func TestBackupRestoreStillExcludesReaders(t *testing.T) {
	b := newBackupGuard()
	started, release, read := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	handler := b.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			close(started)
			<-release
		} else {
			close(read)
		}
	}))
	go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/household/restore", nil))
	<-started
	go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/documents", nil))
	select {
	case <-read:
		t.Fatal("reader ran during restore")
	case <-time.After(30 * time.Millisecond):
	}
	finish()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not resume after restore")
	}
}
