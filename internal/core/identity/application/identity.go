package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

type Config struct {
	PublicURL     string
	SetupCodePath string
	Mailer        Mailer
	Modules       ModuleControl
	// OwnerCapabilities is an explicit application policy, not automatic manifest grants.
	OwnerCapabilities     []string
	DelegableCapabilities []string
}

type Application struct {
	store             Repository
	origin            *url.URL
	ownerCapabilities []string
	capabilities      []string
	setupCodePath     string
	setupCodeHash     []byte
	mailer            Mailer
	mailRequests      chan string
	mailCancel        context.CancelFunc
	mailWorkers       sync.WaitGroup
	modules           ModuleControl
	dummyHash         string
	work              chan struct{}
}

type sessionKey struct{}

func (s *Application) Close() {
	if s.mailCancel != nil {
		s.mailCancel()
		s.mailWorkers.Wait()
	}
}

func (s *Application) LoadSession(ctx context.Context, token string) (*Session, error) {
	if len(token) != 43 {
		return nil, unitofwork.ErrNotFound
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return nil, unitofwork.ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	now := time.Now().Unix()
	current, lastSeen, err := s.store.ReadSessionRecord(ctx, digest[:], now)
	if err != nil {
		return nil, err
	}
	if now-lastSeen >= 60 {
		if _, err := s.store.TouchSession(ctx, nil, now, digest[:]); err != nil {
			return nil, err
		}
	}
	return current, nil
}

func (s *Application) createSession(ctx context.Context, tx unitofwork.Transaction, client Client, userID, householdID string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	digest := sha256.Sum256([]byte(token))
	now := time.Now().Unix()
	if client.HasSession {
		old := sha256.Sum256([]byte(client.SessionToken))
		if _, err := s.store.RotateLoginSession(ctx, tx, old[:]); err != nil {
			return "", err
		}
	}
	if _, err := s.store.ExpireLoginSessions(ctx, tx, now, now-int64(domain.SessionIdle.Seconds())); err != nil {
		return "", err
	}
	_, err := s.store.InsertLoginSession(ctx, tx, digest[:], userID, householdID, now+int64(domain.SessionLifetime.Seconds()), now)
	return token, err
}
