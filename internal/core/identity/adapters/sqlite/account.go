package sqlite

import (
	"context"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

// PIN sessions stop being usable if a role/capability changes to administration.
const ownPINSessionAllowed = ` AND (NOT EXISTS(SELECT 1 FROM identity_pin_sessions ps WHERE ps.token_hash=s.token_hash)
 OR (m.role IN ('member','restricted','guest') AND NOT EXISTS(SELECT 1 FROM identity_permissions ip WHERE ip.user_id=m.user_id AND ip.household_id=m.household_id AND ip.capability LIKE 'household.%')))`

func (s *Repository) ReadOwnAccount(ctx context.Context, tx unitofwork.Transaction, hash []byte, user, home, member string, now, idle int64) (domain.AccountProfile, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return domain.AccountProfile{}, err
	}
	var v domain.AccountProfile
	err = q.QueryRowContext(ctx, `SELECT u.id,u.name,COALESCE(u.email,''),COALESCE(u.username,'u-'||lower(u.id)),EXISTS(SELECT 1 FROM identity_verified_emails v WHERE v.user_id=u.id AND v.email=u.email),u.password_hash!='!'
 FROM identity_sessions s JOIN identity_users u ON u.id=s.user_id JOIN identity_memberships m ON m.user_id=s.user_id AND m.household_id=s.household_id JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id
 WHERE s.token_hash=? AND s.user_id=? AND s.household_id=? AND mi.id=? AND s.expires_at>? AND s.last_seen>?`+ownPINSessionAllowed, hash, user, home, member, now, idle).Scan(&v.User.ID, &v.User.Name, &v.User.Email, &v.User.Username, &v.User.EmailVerified, &v.HasPassword)
	return v, database.Normalize(err)
}

func (s *Repository) SaveOwnName(ctx context.Context, tx unitofwork.Transaction, user, name string) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE identity_users SET name=? WHERE id=?", name, user)
	return err
}

func (s *Repository) ReadOwnSessions(ctx context.Context, tx unitofwork.Transaction, user, home string, current []byte, now, idle int64) ([]domain.AccountSession, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT s.token_hash,s.last_seen,s.expires_at,EXISTS(SELECT 1 FROM identity_pin_sessions ps WHERE ps.token_hash=s.token_hash)
 FROM identity_sessions s JOIN identity_memberships m ON m.user_id=s.user_id AND m.household_id=s.household_id
 WHERE s.user_id=? AND s.household_id=? AND s.expires_at>? AND s.last_seen>?`+ownPINSessionAllowed+` ORDER BY (s.token_hash=?) DESC,s.last_seen DESC,s.token_hash LIMIT 101`, user, home, now, idle, current)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AccountSession{}
	for rows.Next() {
		var v domain.AccountSession
		var seen, expires int64
		var pin bool
		if err := rows.Scan(&v.TokenHash, &seen, &expires, &pin); err != nil {
			return nil, err
		}
		v.LastSeen = time.Unix(seen, 0).UTC()
		v.ExpiresAt = time.Unix(expires, 0).UTC()
		v.Method = "password"
		if pin {
			v.Method = "pin"
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Repository) DeleteOwnSession(ctx context.Context, tx unitofwork.Transaction, user, home string, hash []byte) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "DELETE FROM identity_sessions WHERE user_id=? AND household_id=? AND token_hash=?", user, home, hash)
	return err
}
func (s *Repository) DeleteOtherOwnSessions(ctx context.Context, tx unitofwork.Transaction, user, home string, keep []byte) error {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "DELETE FROM identity_sessions WHERE user_id=? AND household_id=? AND token_hash!=?", user, home, keep)
	return err
}
