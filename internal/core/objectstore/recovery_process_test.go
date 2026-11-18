//go:build unix

package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/database"
)

func TestRemoteUploadCrashChild(t *testing.T) {
	dir := os.Getenv("HOMEATLAS_S3_CRASH_DATA")
	if dir == "" {
		return
	}
	ctx := context.Background()
	db, err := database.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	der, err := os.ReadFile(os.Getenv("HOMEATLAS_S3_CRASH_CA"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	s.SetHTTPClient(&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}, Timeout: 10 * time.Second})
	f, err := os.CreateTemp(dir, "test-input-")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw := []byte("S3 object uploaded before process death")
	if _, err = f.Write(raw); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upload(ctx, "home", "documents", "interrupted", f, int64(len(raw)), fmt.Sprintf("%x", sha256.Sum256(raw))); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("HOMEATLAS_S3_CRASH_MARKER"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestRemoteUploadSIGKILLRecoversDurableIntent(t *testing.T) {
	s, server := fixture(t)
	ctx := context.Background()
	if err := s.Save(ctx, "home", config(server), allow); err != nil {
		t.Fatal(err)
	}
	marker, ca := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "certificate")
	if err := os.WriteFile(ca, server.Certificate().Raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRemoteUploadCrashChild$")
	cmd.Env = append(os.Environ(), "HOMEATLAS_S3_CRASH_DATA="+s.dir, "HOMEATLAS_S3_CRASH_CA="+ca, "HOMEATLAS_S3_CRASH_MARKER="+marker)
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer cmd.Process.Kill()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("child exited: %v %s", err, log.String())
		default:
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			<-done
			t.Fatalf("checkpoint timeout: %s", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("expected killed process")
	}
	if server.Count() != 1 {
		t.Fatal("remote orphan not reproduced", server.Count())
	}
	restarted, err := New(ctx, s.db, s.dir)
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetHTTPClient(server.Client())
	if err = restarted.RecoverPending(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if server.Count() != 0 {
		t.Fatal("remote orphan retained")
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM file_storage_cleanup").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	t.Log("SIGKILL after S3 PUT: durable intent replayed; uncommitted object removed")
}
