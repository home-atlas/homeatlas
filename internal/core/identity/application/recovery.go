package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

func (s *Application) prepareSetupCode(ctx context.Context, path string) error {
	var ready bool
	if err := s.store.ReadSetupState(ctx, nil, &ready); err != nil {
		return err
	}
	s.setupCodePath = path
	if ready {
		s.removeSetupCode()
		return nil
	}
	if path == "" {
		return fmt.Errorf("a private setup code path is required for a new installation")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, err = io.WriteString(f, domain.RandomCode()+"\n")
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	// A damaged or broadly readable file fails closed; never silently replace a setup secret.
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("setup-code must be a private regular file (0600)")
	}
	f, err = os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("setup-code changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 128))
	if err != nil {
		return err
	}
	digest, ok := domain.TokenDigest(strings.TrimSpace(string(raw)))
	if !ok {
		return fmt.Errorf("invalid setup-code file; restore or remove it locally and restart before setup")
	}
	s.setupCodeHash = digest
	return nil
}

func (s *Application) removeSetupCode() {
	if s.setupCodePath == "" {
		return
	}
	if err := os.Remove(s.setupCodePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		// The installation row already prevents reuse even if file cleanup fails.
		slog.Warn("could not remove consumed setup-code file", "error", err)
	}
}

// IssueRecovery is for the local CLI only; email recovery uses a separate token table.
func (s *Application) IssueRecovery(ctx context.Context, email string) (string, time.Time, error) {
	email, ok := domain.NormalizeLogin(email)
	if !ok {
		return "", time.Time{}, fmt.Errorf("invalid account identifier")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()
	var userID string
	err = s.store.ReadLocalRecoveryAccount(ctx, tx, email, email, &userID)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return "", time.Time{}, fmt.Errorf("no active local account matches that identifier")
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("open the installation once with the current server before recovering: %w", err)
	}
	code := domain.RandomCode()
	digest, _ := domain.TokenDigest(code)
	ExpiresUnix := time.Now().Add(domain.RecoveryLifetime).UTC().Truncate(time.Second)
	_, err = s.store.SaveLocalRecovery(ctx, tx, userID, digest, ExpiresUnix.Unix())
	if err != nil {
		return "", time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	return code, ExpiresUnix, nil
}

func (s *Application) RecoverAccess(ctx context.Context, client Client, input RecoverAccessInput) (Result, error) {
	result := Result{}

	if err := s.acquire(); err != nil {
		return result, err
	}
	defer func() { <-s.work }()
	digest, valid := domain.TokenDigest(strings.TrimSpace(input.Code))
	if err := s.limit(ctx, client, "recovery:"+hex.EncodeToString(digest)); err != nil {
		return result, err
	}
	if !valid {
		return result, fault.New(fault.Expired, "recovery_unavailable")
	}
	if !domain.ValidNewPassword(input.Password) {
		return result, fault.New(fault.Invalid, "invalid_password")
	}
	// Avoid password derivation for nonexistent tokens; validity is rechecked atomically below.
	var exists bool
	if err := s.store.IsRecoveryAvailable(ctx, nil, digest, time.Now().Unix(), digest, time.Now().Unix(), &exists); err != nil {
		return result, fault.New(fault.Unavailable, "recovery_unavailable")
	}
	if !exists {
		return result, fault.New(fault.Expired, "recovery_unavailable")
	}
	hash, err := domain.HashPassword(input.Password)
	if err != nil {
		return result, fault.New(fault.Unavailable, "recovery_unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "recovery_unavailable")
	}
	defer tx.Rollback()
	var userID, email string
	err = s.store.ConsumeLocalRecovery(ctx, tx, digest, time.Now().Unix(), &userID)
	if errors.Is(err, unitofwork.ErrNotFound) {
		err = s.store.ConsumeMailRecovery(ctx, tx, digest, time.Now().Unix(), digest, &userID)
	}
	if errors.Is(err, unitofwork.ErrNotFound) {
		return result, fault.New(fault.Expired, "recovery_unavailable")
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "recovery_unavailable")
	}
	if err := s.store.ReplaceRecoveredPassword(ctx, tx, hash, userID, &email); err != nil {
		return result, fault.New(fault.Unavailable, "recovery_unavailable")
	}
	// Revoke all sessions across households and invitations issued before recovery.
	if err := s.store.RevokeRecoveredCredentials(ctx, tx, userID, time.Now().Unix()); err != nil {
		return result, fault.New(fault.Unavailable, "recovery_unavailable")
	}
	accountKey := sha256.Sum256([]byte("account:" + email))
	if _, err := s.store.ClearRecoveredAccountAttempts(ctx, tx, accountKey[:]); err != nil {
		return result, fault.New(fault.Unavailable, "recovery_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "recovery_unavailable")
	}
	result.EndSession = true

	return result, nil
}

func RecoveryFor(ctx context.Context, store Repository, email string) (string, time.Time, error) {
	return (&Application{store: store}).IssueRecovery(ctx, email)
}
