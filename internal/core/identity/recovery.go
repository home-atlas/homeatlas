package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const recoveryLifetime = 15 * time.Minute

func randomCode() string {
	var raw [32]byte
	// crypto/rand.Read terminates the process if the platform CSPRNG fails.
	rand.Read(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func (s *Service) prepareSetupCode(ctx context.Context, path string) error {
	var ready bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_installation)").Scan(&ready); err != nil {
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
		_, err = io.WriteString(f, randomCode()+"\n")
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
	digest, ok := tokenDigest(strings.TrimSpace(string(raw)))
	if !ok {
		return fmt.Errorf("invalid setup-code file; restore or remove it locally and restart before setup")
	}
	s.setupCodeHash = digest
	return nil
}

func (s *Service) removeSetupCode() {
	if s.setupCodePath == "" {
		return
	}
	if err := os.Remove(s.setupCodePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		// The installation row already prevents reuse even if file cleanup fails.
		slog.Warn("could not remove consumed setup-code file", "error", err)
	}
}

// IssueRecovery is for the local CLI only; email recovery uses a separate token table.
func IssueRecovery(ctx context.Context, db *sql.DB, email string) (string, time.Time, error) {
	email, ok := normalizeLogin(email)
	if !ok {
		return "", time.Time{}, fmt.Errorf("invalid account identifier")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()
	var userID string
	err = tx.QueryRowContext(ctx, `SELECT u.id FROM identity_users u WHERE (u.email=? OR u.username=?)
 AND EXISTS(SELECT 1 FROM identity_installation)
 AND EXISTS(SELECT 1 FROM identity_memberships m WHERE m.user_id=u.id)`, email, email).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, fmt.Errorf("no active local account matches that identifier")
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("open the installation once with the current server before recovering: %w", err)
	}
	code := randomCode()
	digest, _ := tokenDigest(code)
	expires := time.Now().Add(recoveryLifetime).UTC().Truncate(time.Second)
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_recovery(user_id,token_hash,expires_at) VALUES(?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET token_hash=excluded.token_hash,expires_at=excluded.expires_at`, userID, digest, expires.Unix())
	if err != nil {
		return "", time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

func (s *Service) recoverAccess(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !s.acquire(w) {
		return
	}
	defer func() { <-s.work }()
	digest, valid := tokenDigest(strings.TrimSpace(input.Code))
	if !s.limit(w, r, "recovery:"+hex.EncodeToString(digest)) {
		return
	}
	if !valid {
		fail(w, 410, "recovery_unavailable")
		return
	}
	if !validNewPassword(input.Password) {
		fail(w, 400, "invalid_password")
		return
	}
	// Avoid password derivation for nonexistent tokens; validity is rechecked atomically below.
	var exists bool
	if err := s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM identity_recovery WHERE token_hash=? AND expires_at>?) OR EXISTS(`+validMailRecovery+`)`, digest, time.Now().Unix(), digest, time.Now().Unix()).Scan(&exists); err != nil {
		fail(w, 503, "recovery_unavailable")
		return
	}
	if !exists {
		fail(w, 410, "recovery_unavailable")
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		fail(w, 503, "recovery_unavailable")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "recovery_unavailable")
		return
	}
	defer tx.Rollback()
	var userID, email string
	err = tx.QueryRowContext(r.Context(), `DELETE FROM identity_recovery WHERE token_hash=? AND expires_at>?
 AND EXISTS(SELECT 1 FROM identity_memberships m WHERE m.user_id=identity_recovery.user_id) RETURNING user_id`, digest, time.Now().Unix()).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(r.Context(), `DELETE FROM identity_mail_recovery WHERE user_id IN (`+validMailRecovery+`) AND token_hash=? RETURNING user_id`, digest, time.Now().Unix(), digest).Scan(&userID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 410, "recovery_unavailable")
		return
	}
	if err != nil {
		fail(w, 503, "recovery_unavailable")
		return
	}
	if err := tx.QueryRowContext(r.Context(), `UPDATE identity_users SET password_hash=? WHERE id=? RETURNING COALESCE(email,username)`, hash, userID).Scan(&email); err != nil {
		fail(w, 503, "recovery_unavailable")
		return
	}
	// Revoke all sessions across households and invitations issued before recovery.
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM identity_recovery WHERE user_id=?`, []any{userID}},
		{`DELETE FROM identity_mail_recovery WHERE user_id=?`, []any{userID}},
		{`DELETE FROM identity_email_verification WHERE user_id=?`, []any{userID}},
		{`DELETE FROM identity_sessions WHERE user_id=?`, []any{userID}},
		{`DELETE FROM identity_pin_devices WHERE user_id=?`, []any{userID}},
		{`DELETE FROM identity_assisted_enrollments WHERE user_id=?`, []any{userID}},
		{`UPDATE identity_invitations SET revoked_at=? WHERE issuer_id=? AND accepted_at=0 AND revoked_at=0`, []any{time.Now().Unix(), userID}},
	} {
		if _, err := tx.ExecContext(r.Context(), statement.query, statement.args...); err != nil {
			fail(w, 503, "recovery_unavailable")
			return
		}
	}
	accountKey := sha256.Sum256([]byte("account:" + email))
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM identity_login_attempts WHERE key=?`, accountKey[:]); err != nil {
		fail(w, 503, "recovery_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "recovery_unavailable")
		return
	}
	s.clearCookie(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
