package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/modules"
)

type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

const verificationLifetime = time.Hour

func (s *Service) requestVerification(w http.ResponseWriter, r *http.Request) {
	current, _ := r.Context().Value(sessionKey{}).(*Session)
	if current == nil {
		fail(w, 401, "authentication_required")
		return
	}
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	if current.User.Email == "" {
		fail(w, 400, "email_required")
		return
	}
	if s.mailer == nil {
		fail(w, 503, "smtp_disabled")
		return
	}
	if !s.limit(w, r, "verify:"+current.User.ID) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "email_unavailable")
		return
	}
	defer tx.Rollback()
	// Check the session again under the transaction so recovery cannot leave a stale verifier.
	cookie, _ := r.Cookie(s.cookieName)
	if cookie == nil {
		fail(w, 401, "authentication_required")
		return
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	var email string
	err = tx.QueryRowContext(r.Context(), `SELECT u.email FROM identity_users u JOIN identity_sessions s ON s.user_id=u.id
 WHERE u.id=? AND s.token_hash=? AND s.expires_at>? AND s.last_seen>?`, current.User.ID, digest[:], time.Now().Unix(), time.Now().Unix()-int64(sessionIdle.Seconds())).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "authentication_required")
		return
	}
	if err != nil {
		fail(w, 503, "email_unavailable")
		return
	}
	code := randomCode()
	hash, _ := tokenDigest(code)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO identity_email_verification(user_id,email,token_hash,expires_at) VALUES(?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,token_hash=excluded.token_hash,expires_at=excluded.expires_at`, current.User.ID, email, hash, time.Now().Add(verificationLifetime).Unix())
	if err != nil {
		fail(w, 503, "email_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "email_unavailable")
		return
	}
	body := fmt.Sprintf("Confirma tu correo en HomeAtlas abriendo este enlace y pulsando Verificar correo:\n\n%s/verify-email#%s\n\nCaduca en 1 hora y solo puede usarse una vez. Si no lo solicitaste, ignora este mensaje.\n", s.origin.String(), code)
	if err := s.mailer.Send(r.Context(), email, "Verifica tu correo en HomeAtlas", body); err != nil {
		// Do not expose relay responses, addresses or bearer tokens in API errors/logs.
		slog.Warn("verification email could not be accepted by SMTP relay")
		fail(w, 503, "email_unavailable")
		return
	}
	modules.JSON(w, 202, map[string]string{"status": "accepted_by_relay"})
}

func (s *Service) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &input) {
		return
	}
	hash, ok := tokenDigest(input.Code)
	if !s.limit(w, r, fmt.Sprintf("verify-consume:%x", hash)) {
		return
	}
	if !ok {
		fail(w, 410, "verification_unavailable")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "email_unavailable")
		return
	}
	defer tx.Rollback()
	var user, email string
	err = tx.QueryRowContext(r.Context(), `DELETE FROM identity_email_verification WHERE token_hash=? AND expires_at>?
 AND EXISTS(SELECT 1 FROM identity_users u JOIN identity_memberships m ON m.user_id=u.id WHERE u.id=identity_email_verification.user_id AND u.email=identity_email_verification.email)
 RETURNING user_id,email`, hash, time.Now().Unix()).Scan(&user, &email)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 410, "verification_unavailable")
		return
	}
	if err != nil {
		fail(w, 503, "email_unavailable")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO identity_verified_emails(user_id,email,verified_at) VALUES(?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,verified_at=excluded.verified_at`, user, email, time.Now().Unix()); err != nil {
		fail(w, 503, "email_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 503, "email_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

func (s *Service) requestRecoveryEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &input) {
		return
	}
	if s.mailer == nil {
		fail(w, 503, "smtp_disabled")
		return
	}
	email, ok := normalizeEmail(input.Email)
	if !ok {
		fail(w, 400, "invalid_email")
		return
	}
	if !s.limit(w, r, "mail-recovery:"+email) {
		return
	}
	// Every syntactically valid email follows the same bounded queue path, without an account lookup.
	// shortcut: this transient queue has no retries across restarts; request another email or use local recovery.
	select {
	case s.mailRequests <- email:
	default:
	}
	modules.JSON(w, 202, map[string]string{"status": "requested"})
}

func (s *Service) recoveryMailWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case email := <-s.mailRequests:
			if err := s.sendRecoveryEmail(ctx, email); err != nil && ctx.Err() == nil {
				slog.Warn("recovery email could not be accepted by SMTP relay")
			}
		}
	}
}

func (s *Service) sendRecoveryEmail(ctx context.Context, email string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var user string
	err = tx.QueryRowContext(ctx, `SELECT u.id FROM identity_users u JOIN identity_verified_emails v ON v.user_id=u.id AND v.email=u.email
 WHERE u.email=? AND EXISTS(SELECT 1 FROM identity_memberships m WHERE m.user_id=u.id)`, email).Scan(&user)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	code := randomCode()
	hash, _ := tokenDigest(code)
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_mail_recovery(user_id,email,token_hash,expires_at) VALUES(?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET email=excluded.email,token_hash=excluded.token_hash,expires_at=excluded.expires_at`, user, email, hash, time.Now().Add(recoveryLifetime).Unix())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	body := fmt.Sprintf("Recupera el acceso a tu cuenta local de HomeAtlas:\n\n%s/forgot-password#%s\n\nCaduca en 15 minutos y solo puede usarse una vez. También puedes introducir este código en /forgot-password:\n%s\n\nSolo al cambiar la contraseña se cerrarán tus sesiones y se anularán tus invitaciones pendientes. Si no lo solicitaste, ignora este mensaje.\n", s.origin.String(), code, code)
	return s.mailer.Send(ctx, email, "Recupera tu acceso a HomeAtlas", body)
}

const validMailRecovery = `SELECT t.user_id FROM identity_mail_recovery t
 JOIN identity_users u ON u.id=t.user_id AND u.email=t.email
 JOIN identity_verified_emails v ON v.user_id=u.id AND v.email=u.email
 WHERE t.token_hash=? AND t.expires_at>?
 AND EXISTS(SELECT 1 FROM identity_memberships m WHERE m.user_id=u.id)`
