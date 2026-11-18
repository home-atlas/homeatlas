package sqlite

import (
	"context"
	"database/sql"

	"github.com/home-atlas/homeatlas/internal/core/database"
)

func Migrate(ctx context.Context, db *sql.DB) error {
	return database.Apply(ctx, db, "core.identity", []database.Migration{{Version: 1, SQL: `
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
}
