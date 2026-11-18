package identity

import (
	"database/sql"

	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
)

func identityDB(s *Service) *sql.DB { return s.database }

const (
	sessionLifetime  = domain.SessionLifetime
	sessionIdle      = domain.SessionIdle
	recoveryLifetime = domain.RecoveryLifetime
)

var (
	verifyPassword   = domain.VerifyPassword
	tokenDigest      = domain.TokenDigest
	validNewPassword = domain.ValidNewPassword
)

var validText = domain.ValidText
