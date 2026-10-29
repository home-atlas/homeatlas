package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/modules"
)

const (
	sessionLifetime = 12 * time.Hour
	sessionIdle     = 30 * time.Minute
	attemptWindow   = 15 * time.Minute
)

type Config struct {
	PublicURL     string
	SetupCodePath string
	Mailer        Mailer
	Modules       *modules.Registry
	// OwnerCapabilities is an explicit application policy, not automatic manifest grants.
	OwnerCapabilities     []string
	DelegableCapabilities []string
}

type Service struct {
	db                *sql.DB
	origin            *url.URL
	secure            bool
	cookieName        string
	ownerCapabilities []string
	capabilities      []string
	setupCodePath     string
	setupCodeHash     []byte
	mailer            Mailer
	mailRequests      chan string
	mailCancel        context.CancelFunc
	mailWorkers       sync.WaitGroup
	modules           *modules.Registry
	dummyHash         string
	work              chan struct{}
	csrf              *http.CrossOriginProtection
}

type User struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"emailVerified"`
	Username      string `json:"username"`
}

type Household struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

type Session struct {
	User         User      `json:"user"`
	Household    Household `json:"household"`
	Role         string    `json:"role"`
	Permissions  []string  `json:"permissions"`
	MembershipID string    `json:"-"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type State struct {
	SetupRequired bool     `json:"setupRequired"`
	Session       *Session `json:"session"`
	SMTPEnabled   bool     `json:"smtpEnabled"`
}

type sessionKey struct{}

func New(ctx context.Context, db *sql.DB, config Config) (*Service, error) {
	origin, err := url.Parse(config.PublicURL)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, fmt.Errorf("public URL must be an http(s) origin without a path")
	}
	loopback := origin.Hostname() == "localhost"
	if ip := net.ParseIP(origin.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if origin.Scheme == "http" && !loopback {
		return nil, fmt.Errorf("public URL requires HTTPS outside loopback")
	}
	s := &Service{db: db, origin: origin, secure: origin.Scheme == "https", cookieName: "homeatlas_session",
		capabilities: append([]string(nil), config.DelegableCapabilities...), ownerCapabilities: append([]string(nil), config.OwnerCapabilities...), work: make(chan struct{}, 2), csrf: http.NewCrossOriginProtection()}
	s.modules = config.Modules
	if s.secure {
		s.cookieName = "__Host-homeatlas_session"
	}
	if err := s.csrf.AddTrustedOrigin(config.PublicURL); err != nil {
		return nil, err
	}
	err = database.Apply(ctx, db, "core.identity", []database.Migration{{Version: 1, SQL: `
		CREATE TABLE identity_users (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash TEXT NOT NULL);
		CREATE TABLE identity_households (id TEXT PRIMARY KEY, name TEXT NOT NULL, timezone TEXT NOT NULL);
		CREATE TABLE identity_memberships (
			user_id TEXT NOT NULL REFERENCES identity_users(id) ON DELETE CASCADE,
			household_id TEXT NOT NULL REFERENCES identity_households(id) ON DELETE CASCADE,
			role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'restricted', 'guest')),
			PRIMARY KEY(user_id, household_id));
		CREATE TABLE identity_permissions (
			user_id TEXT NOT NULL, household_id TEXT NOT NULL, capability TEXT NOT NULL,
			PRIMARY KEY(user_id, household_id, capability),
			FOREIGN KEY(user_id, household_id) REFERENCES identity_memberships(user_id, household_id) ON DELETE CASCADE);
		CREATE TABLE identity_sessions (
			token_hash BLOB PRIMARY KEY, user_id TEXT NOT NULL, household_id TEXT NOT NULL,
			expires_at INTEGER NOT NULL, last_seen INTEGER NOT NULL,
			FOREIGN KEY(user_id, household_id) REFERENCES identity_memberships(user_id, household_id) ON DELETE CASCADE);
		CREATE INDEX identity_sessions_expiry ON identity_sessions(expires_at);
		CREATE TABLE identity_installation (singleton INTEGER PRIMARY KEY CHECK (singleton = 1));
		CREATE TABLE identity_login_attempts (key BLOB PRIMARY KEY, window_start INTEGER NOT NULL, attempts INTEGER NOT NULL);
	`}, {Version: 2, SQL: `
 CREATE TABLE identity_member_ids (
   user_id TEXT NOT NULL, household_id TEXT NOT NULL, id TEXT NOT NULL UNIQUE,
   PRIMARY KEY(user_id,household_id),
   FOREIGN KEY(user_id,household_id) REFERENCES identity_memberships(user_id,household_id) ON DELETE CASCADE);
 INSERT INTO identity_member_ids SELECT user_id,household_id,lower(hex(randomblob(16))) FROM identity_memberships;
 CREATE TRIGGER identity_member_created AFTER INSERT ON identity_memberships BEGIN
   INSERT INTO identity_member_ids VALUES(new.user_id,new.household_id,lower(hex(randomblob(16)))); END;
 CREATE TABLE identity_invitations (
   id TEXT PRIMARY KEY, household_id TEXT NOT NULL REFERENCES identity_households(id) ON DELETE CASCADE,
   email TEXT NOT NULL COLLATE NOCASE, role TEXT NOT NULL CHECK(role IN ('admin','member','restricted','guest')),
   permissions TEXT NOT NULL, token_hash BLOB NOT NULL UNIQUE,
   issuer_id TEXT NOT NULL, issuer_member_id TEXT NOT NULL,
   created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, accepted_at INTEGER NOT NULL DEFAULT 0,
   revoked_at INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX identity_invitations_household ON identity_invitations(household_id,created_at);
 `}, {Version: 3, SQL: `
 CREATE TABLE identity_recovery (
   user_id TEXT PRIMARY KEY REFERENCES identity_users(id) ON DELETE CASCADE,
   token_hash BLOB NOT NULL UNIQUE, expires_at INTEGER NOT NULL);
 `}, {Version: 4, SQL: `
 CREATE TABLE identity_verified_emails (
   user_id TEXT PRIMARY KEY REFERENCES identity_users(id) ON DELETE CASCADE,
   email TEXT NOT NULL COLLATE NOCASE, verified_at INTEGER NOT NULL);
 CREATE TABLE identity_email_verification (
   user_id TEXT PRIMARY KEY REFERENCES identity_users(id) ON DELETE CASCADE,
   email TEXT NOT NULL COLLATE NOCASE, token_hash BLOB NOT NULL UNIQUE, expires_at INTEGER NOT NULL);
 CREATE TABLE identity_mail_recovery (
   user_id TEXT PRIMARY KEY REFERENCES identity_users(id) ON DELETE CASCADE,
   email TEXT NOT NULL COLLATE NOCASE, token_hash BLOB NOT NULL UNIQUE, expires_at INTEGER NOT NULL);
 `}, {Version: 5, RebuildTables: true, SQL: `
 CREATE TABLE identity_users_new (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT COLLATE NOCASE UNIQUE,
  password_hash TEXT NOT NULL, username TEXT UNIQUE COLLATE NOCASE,
  assisted INTEGER NOT NULL DEFAULT 0 CHECK(assisted IN (0,1)));
 INSERT INTO identity_users_new(id,name,email,password_hash,username)
  SELECT id,name,email,password_hash,'u-'||lower(id) FROM identity_users;
 DROP TABLE identity_users;
 ALTER TABLE identity_users_new RENAME TO identity_users;
 CREATE TABLE identity_pin_sessions (token_hash BLOB PRIMARY KEY REFERENCES identity_sessions(token_hash) ON DELETE CASCADE);
 CREATE TABLE identity_access_events (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, household_id TEXT NOT NULL, actor_id TEXT NOT NULL, action TEXT NOT NULL, created_at INTEGER NOT NULL);
 CREATE TABLE identity_pin_devices (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, household_id TEXT NOT NULL,
  token_hash BLOB NOT NULL UNIQUE, pin_hash TEXT NOT NULL, name TEXT NOT NULL,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, failures INTEGER NOT NULL DEFAULT 0,
  FOREIGN KEY(user_id,household_id) REFERENCES identity_memberships(user_id,household_id) ON DELETE CASCADE);
 CREATE TABLE identity_assisted_enrollments (
  user_id TEXT PRIMARY KEY, household_id TEXT NOT NULL, token_hash BLOB NOT NULL UNIQUE,
  issuer_id TEXT NOT NULL, issuer_member_id TEXT NOT NULL, expires_at INTEGER NOT NULL,
  FOREIGN KEY(user_id,household_id) REFERENCES identity_memberships(user_id,household_id) ON DELETE CASCADE);
 `}})
	if err != nil {
		return nil, err
	}
	if err := s.prepareSetupCode(ctx, config.SetupCodePath); err != nil {
		return nil, err
	}
	s.dummyHash, err = hashPassword(rand.Text())
	if err == nil && config.Mailer != nil {
		s.mailer = config.Mailer
		s.mailRequests = make(chan string, 16)
		mailCtx, cancel := context.WithCancel(ctx)
		s.mailCancel = cancel
		for range 2 {
			s.mailWorkers.Go(func() { s.recoveryMailWorker(mailCtx) })
		}
	}
	return s, err
}

func (s *Service) Close() {
	if s.mailCancel != nil {
		s.mailCancel()
		s.mailWorkers.Wait()
	}
}

func (s *Service) Mount(mux *http.ServeMux) {
	s.mountMembers(mux)
	s.mountAlternativeAccess(mux)
	mux.HandleFunc("GET /api/household/settings", s.householdSettings)
	mux.HandleFunc("PATCH /api/household/settings", s.updateHouseholdSettings)
	if s.modules != nil {
		mux.HandleFunc("PATCH /api/household/modules/{id}", s.updateModule)
	}
	mux.HandleFunc("GET /api/auth/session", s.session)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("POST /api/auth/recover", s.recoverAccess)
	mux.HandleFunc("POST /api/auth/email/verification", s.requestVerification)
	mux.HandleFunc("POST /api/auth/email/verify", s.verifyEmail)
	mux.HandleFunc("POST /api/auth/recovery/request", s.requestRecoveryEmail)
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// Do not trust Forwarded headers or an attacker-controlled Host (including DNS rebinding).
		if r.Host != s.origin.Host {
			fail(w, 421, "unexpected_host")
			return
		}
		if err := s.csrf.Check(r); err != nil {
			fail(w, 403, "cross_origin_request")
			return
		}
		ctx := access.WithPrincipal(r.Context(), access.Principal{})
		if cookie, err := r.Cookie(s.cookieName); err == nil {
			current, err := s.load(r.Context(), cookie.Value)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				s.clearCookie(w)
			case err != nil:
				fail(w, 503, "session_unavailable")
				return
			default:
				grants := make(map[string]bool, len(current.Permissions))
				for _, permission := range current.Permissions {
					grants[permission] = true
				}
				ctx = access.WithPrincipal(ctx, access.Principal{MembershipID: current.MembershipID, UserID: current.User.ID, HouseholdID: current.Household.ID, Permissions: grants})
				ctx = context.WithValue(ctx, sessionKey{}, current)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) load(ctx context.Context, token string) (*Session, error) {
	if len(token) != 43 {
		return nil, sql.ErrNoRows
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return nil, sql.ErrNoRows
	}
	digest := sha256.Sum256([]byte(token))
	now := time.Now().Unix()
	current := &Session{Permissions: []string{}}
	var expires, lastSeen int64
	err = s.db.QueryRowContext(ctx, `SELECT u.id, u.name, COALESCE(u.email,''), h.id, h.name, h.timezone, m.role, s.expires_at, s.last_seen, mi.id,
        EXISTS(SELECT 1 FROM identity_verified_emails v WHERE v.user_id=u.id AND v.email=u.email), COALESCE(u.username,'u-'||lower(u.id))
		FROM identity_sessions s
		JOIN identity_users u ON u.id = s.user_id
		JOIN identity_memberships m ON m.user_id = s.user_id AND m.household_id = s.household_id
		JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id
		JOIN identity_households h ON h.id = m.household_id
		WHERE s.token_hash = ? AND s.expires_at > ? AND s.last_seen > ?
 AND (NOT EXISTS(SELECT 1 FROM identity_pin_sessions ps WHERE ps.token_hash=s.token_hash) OR (m.role IN ('member','restricted','guest') AND NOT EXISTS(SELECT 1 FROM identity_permissions ip WHERE ip.user_id=m.user_id AND ip.household_id=m.household_id AND ip.capability LIKE 'household.%')))`, digest[:], now, now-int64(sessionIdle.Seconds())).Scan(
		&current.User.ID, &current.User.Name, &current.User.Email, &current.Household.ID, &current.Household.Name, &current.Household.Timezone, &current.Role, &expires, &lastSeen, &current.MembershipID, &current.User.EmailVerified, &current.User.Username)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT capability FROM identity_permissions WHERE user_id = ? AND household_id = ? ORDER BY capability", current.User.ID, current.Household.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			rows.Close()
			return nil, err
		}
		current.Permissions = append(current.Permissions, permission)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if now-lastSeen >= 60 {
		if _, err := s.db.ExecContext(ctx, "UPDATE identity_sessions SET last_seen = MAX(last_seen, ?) WHERE token_hash = ?", now, digest[:]); err != nil {
			return nil, err
		}
	}
	current.ExpiresAt = time.Unix(expires, 0).UTC()
	return current, nil
}

func (s *Service) createSession(ctx context.Context, tx *sql.Tx, r *http.Request, userID, householdID string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	digest := sha256.Sum256([]byte(token))
	now := time.Now().Unix()
	if cookie, err := r.Cookie(s.cookieName); err == nil {
		old := sha256.Sum256([]byte(cookie.Value))
		if _, err := tx.ExecContext(ctx, "DELETE FROM identity_sessions WHERE token_hash = ?", old[:]); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM identity_sessions WHERE expires_at <= ? OR last_seen <= ?", now, now-int64(sessionIdle.Seconds())); err != nil {
		return "", err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO identity_sessions (token_hash, user_id, household_id, expires_at, last_seen) VALUES (?, ?, ?, ?, ?)", digest[:], userID, householdID, now+int64(sessionLifetime.Seconds()), now)
	return token, err
}

func (s *Service) respondSession(w http.ResponseWriter, r *http.Request, token string, status int) {
	current, err := s.load(r.Context(), token)
	if err != nil {
		fail(w, 500, "session_unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteStrictMode, Expires: current.ExpiresAt, MaxAge: int(sessionLifetime.Seconds())})
	modules.JSON(w, status, State{Session: current, SMTPEnabled: s.mailer != nil})
}

func (s *Service) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteStrictMode, Expires: time.Unix(1, 0), MaxAge: -1})
}

func fail(w http.ResponseWriter, status int, code string) {
	modules.JSON(w, status, map[string]string{"error": code})
}
