package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity"
)

func TestRecoveryCommandUsesExistingInstallationAndReplacesCode(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db, err := database.Open(filepath.Join(dir, "homeatlas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := identity.New(ctx, db, identity.Config{PublicURL: "http://127.0.0.1:8080", SetupCodePath: filepath.Join(dir, "setup-code")})
	if err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join(dir, "setup-code"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": strings.TrimSpace(string(code)), "name": "Owner", "email": "owner@example.test", "password": "a local private test passphrase", "householdName": "Home", "timezone": "UTC"})
	mux := http.NewServeMux()
	s.Mount(mux)
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/setup", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Middleware(mux).ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var old, username string
	if err := db.QueryRow("SELECT username FROM identity_users WHERE email=?", "owner@example.test").Scan(&username); err != nil {
		t.Fatal(err)
	}
	for _, account := range []struct{ flag, value string }{{"-email", "owner@example.test"}, {"-account", username}} {
		var out, stderr bytes.Buffer
		if err := recoveryCommand(ctx, []string{"-data", dir, account.flag, account.value}, &out, &stderr); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(out.String(), "\n")
		if len(lines) < 3 || len(lines[1]) != 43 || lines[1] == old || !strings.Contains(out.String(), "/forgot-password") || stderr.Len() != 0 {
			t.Fatal("invalid CLI output")
		}
		old = lines[1]
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM identity_recovery`).Scan(&count)
	if count != 1 {
		t.Fatal("CLI did not replace recovery code")
	}
	if err := recoveryCommand(ctx, []string{"-data", dir, "-email", "unknown@example.test"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("CLI recovered nonexistent account")
	}
}

func TestRecoveryCommandRejectsMissingDataAndArguments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	for _, args := range [][]string{{}, {"-data", dir, "-email", "owner@example.test"}, {"-email", "owner@example.test", "unexpected"}, {"-unknown"}} {
		if err := recoveryCommand(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid CLI accepted", args)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("recovery created an empty data directory")
	}
}
