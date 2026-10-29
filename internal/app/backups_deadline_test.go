package app

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	err      error
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return w.err
}
func TestBackupResponseDeadlineIsFiniteAndReportsTransportErrors(t *testing.T) {
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now()
	if err := backupResponseDeadline(w); err != nil {
		t.Fatal(err)
	}
	if d := w.deadline.Sub(before); d < 10*time.Minute || d > 10*time.Minute+time.Second {
		t.Fatal("unexpected response deadline", d)
	}
	w.err = errors.New("transport failed")
	if err := backupResponseDeadline(w); !errors.Is(err, w.err) {
		t.Fatal("transport error ignored", err)
	}
	if err := backupResponseDeadline(httptest.NewRecorder()); err != nil {
		t.Fatal("recorder unsupported controller", err)
	}
}
func TestBackupResponseCanOutlastNormalServerWriteTimeout(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := backupResponseDeadline(w); err != nil {
			http.Error(w, "deadline error", 500)
			return
		}
		time.Sleep(60 * time.Millisecond)
		w.Write([]byte("complete"))
	}))
	server.Config.WriteTimeout = 20 * time.Millisecond
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Second
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(body) != "complete" {
		t.Fatal(response.StatusCode, string(body), err)
	}
}

func TestReaderQueuedBehindRestoreGetsFreshResponseDeadline(t *testing.T) {
	b := &householdBackups{}
	b.mu.Lock()
	locked := true
	defer func() {
		if locked {
			b.mu.Unlock()
		}
	}()
	started := make(chan struct{})
	handler := b.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("complete")) }))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); handler.ServeHTTP(w, r) }))
	server.Config.WriteTimeout = 20 * time.Millisecond
	server.Config.ReadTimeout = 20 * time.Millisecond
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Second
	done := make(chan error, 1)
	go func() {
		response, err := client.Get(server.URL)
		if err != nil {
			done <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err == nil && (response.StatusCode != 200 || string(body) != "complete") {
			err = errors.New("queued reader lost its response")
		}
		done <- err
	}()
	<-started
	time.Sleep(60 * time.Millisecond)
	b.mu.Unlock()
	locked = false
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
