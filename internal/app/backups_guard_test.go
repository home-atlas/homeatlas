package app

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestBackupExportDoesNotQueueDeclaredPluginQueries(t *testing.T) {
	b := &householdBackups{readOnlyActions: map[string]bool{"POST /api/ext-test/actions/read": true}}
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
			b := &householdBackups{}
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
	b := &householdBackups{}
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
