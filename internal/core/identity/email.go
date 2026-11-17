package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/fault"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

const verificationLifetime = time.Hour

func (s *Application) RequestVerification(ctx context.Context, client Client) (Result, error) {
	result := Result{}

	current, _ := ctx.Value(sessionKey{}).(*Session)
	if current == nil {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	if current.User.Email == "" {
		return result, fault.New(fault.Invalid, "email_required")
	}
	if s.mailer == nil {
		return result, fault.New(fault.Unavailable, "smtp_disabled")
	}
	if err := s.limit(ctx, client, "verify:"+current.User.ID); err != nil {
		return result, err
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}
	defer tx.Rollback()
	// Check the session again under the transaction so recovery cannot leave a stale verifier.
	if !client.HasSession {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	digest := sha256.Sum256([]byte(client.SessionToken))
	var email string
	err = s.store.readVerificationMailbox(ctx, tx, current.User.ID, digest[:], time.Now().Unix(), time.Now().Unix()-int64(sessionIdle.Seconds()), &email)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return result, fault.New(fault.Unauthenticated, "authentication_required")
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}
	code := randomCode()
	hash, _ := tokenDigest(code)
	_, err = s.store.saveMailboxVerification(ctx, tx, current.User.ID, email, hash, time.Now().Add(verificationLifetime).Unix())
	if err != nil {
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}
	body := fmt.Sprintf("Confirma tu correo en HomeAtlas abriendo este enlace y pulsando Verificar correo:\n\n%s/verify-email#%s\n\nCaduca en 1 hora y solo puede usarse una vez. Si no lo solicitaste, ignora este mensaje.\n", s.origin.String(), code)
	if err := s.mailer.Send(ctx, email, "Verifica tu correo en HomeAtlas", body); err != nil {
		// Do not expose relay responses, addresses or bearer tokens in API errors/logs.
		slog.Warn("verification email could not be accepted by SMTP relay")
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}
	result.Data = map[string]string{"status": "accepted_by_relay"}

	return result, nil
}

func (s *Application) VerifyEmail(ctx context.Context, client Client, input VerifyEmailInput) (Result, error) {
	result := Result{}

	hash, ok := tokenDigest(input.Code)
	if err := s.limit(ctx, client, fmt.Sprintf("verify-consume:%x", hash)); err != nil {
		return result, err
	}
	if !ok {
		return result, fault.New(fault.Expired, "verification_unavailable")
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}
	defer tx.Rollback()
	var user, email string
	err = s.store.consumeMailboxVerification(ctx, tx, hash, time.Now().Unix(), &user, &email)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return result, fault.New(fault.Expired, "verification_unavailable")
	}
	if err != nil {
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}
	if _, err := s.store.saveVerifiedMailbox(ctx, tx, user, email, time.Now().Unix()); err != nil {
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}
	if err := tx.Commit(); err != nil {
		return result, fault.New(fault.Unavailable, "email_unavailable")
	}

	return result, nil
}

func (s *Application) RequestRecoveryEmail(ctx context.Context, client Client, input RequestRecoveryEmailInput) (Result, error) {
	result := Result{}

	if s.mailer == nil {
		return result, fault.New(fault.Unavailable, "smtp_disabled")
	}
	email, ok := normalizeEmail(input.Email)
	if !ok {
		return result, fault.New(fault.Invalid, "invalid_email")
	}
	if err := s.limit(ctx, client, "mail-recovery:"+email); err != nil {
		return result, err
	}
	// Every syntactically valid email follows the same bounded queue path, without an account lookup.
	// shortcut: this transient queue has no retries across restarts; request another email or use local recovery.
	select {
	case s.mailRequests <- email:
	default:
	}
	result.Data = map[string]string{"status": "requested"}

	return result, nil
}

func (s *Application) recoveryMailWorker(ctx context.Context) {
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

func (s *Application) sendRecoveryEmail(ctx context.Context, email string) error {
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var user string
	err = s.store.readVerifiedRecoveryAccount(ctx, tx, email, &user)
	if errors.Is(err, unitofwork.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	code := randomCode()
	hash, _ := tokenDigest(code)
	_, err = s.store.saveMailRecovery(ctx, tx, user, email, hash, time.Now().Add(recoveryLifetime).Unix())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	body := fmt.Sprintf("Recupera el acceso a tu cuenta local de HomeAtlas:\n\n%s/forgot-password#%s\n\nCaduca en 15 minutos y solo puede usarse una vez. También puedes introducir este código en /forgot-password:\n%s\n\nSolo al cambiar la contraseña se cerrarán tus sesiones y se anularán tus invitaciones pendientes. Si no lo solicitaste, ignora este mensaje.\n", s.origin.String(), code, code)
	return s.mailer.Send(ctx, email, "Recupera tu acceso a HomeAtlas", body)
}
