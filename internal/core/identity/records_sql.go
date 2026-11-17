package identity

import (
	"context"
	"encoding/json"
	"time"

	"github.com/home-atlas/homeatlas/internal/core/access"
	"github.com/home-atlas/homeatlas/internal/core/database"
	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

func (s *sqliteRepository) readMember(ctx context.Context, tx unitofwork.Transaction, household, user string) (access.Member, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return access.Member{}, err
	}
	m := access.Member{Permissions: []string{}}
	err = q.QueryRowContext(ctx, `SELECT u.id,mi.id,u.name,COALESCE(u.email,''),m.role,COALESCE(u.username,'u-'||lower(u.id)),u.assisted=1 AND u.password_hash='!' FROM identity_memberships m
 JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id
 JOIN identity_users u ON u.id=m.user_id WHERE m.household_id=? AND m.user_id=?`, household, user).Scan(&m.ID, &m.MembershipID, &m.Name, &m.Email, &m.Role, &m.Username, &m.Assisted)
	if err != nil {
		return m, database.Normalize(err)
	}
	rows, err := q.QueryContext(ctx, `SELECT capability FROM identity_permissions WHERE household_id=? AND user_id=? ORDER BY capability`, household, user)
	if err != nil {
		return m, database.Normalize(err)
	}
	defer rows.Close()
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return m, database.Normalize(err)
		}
		m.Permissions = append(m.Permissions, permission)
	}
	return m, rows.Err()
}

func (s *sqliteRepository) readSessionRecord(ctx context.Context, digest []byte, now int64) (*Session, int64, error) {
	current := &Session{Permissions: []string{}}
	var expires, lastSeen int64
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.name, COALESCE(u.email,''), h.id, h.name, h.timezone, m.role, s.expires_at, s.last_seen, mi.id,
        EXISTS(SELECT 1 FROM identity_verified_emails v WHERE v.user_id=u.id AND v.email=u.email), COALESCE(u.username,'u-'||lower(u.id))
		FROM identity_sessions s
		JOIN identity_users u ON u.id = s.user_id
		JOIN identity_memberships m ON m.user_id = s.user_id AND m.household_id = s.household_id
		JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id
		JOIN identity_households h ON h.id = m.household_id
		WHERE s.token_hash = ? AND s.expires_at > ? AND s.last_seen > ?
 AND (NOT EXISTS(SELECT 1 FROM identity_pin_sessions ps WHERE ps.token_hash=s.token_hash) OR (m.role IN ('member','restricted','guest') AND NOT EXISTS(SELECT 1 FROM identity_permissions ip WHERE ip.user_id=m.user_id AND ip.household_id=m.household_id AND ip.capability LIKE 'household.%')))`, digest, now, now-int64(sessionIdle.Seconds())).Scan(
		&current.User.ID, &current.User.Name, &current.User.Email, &current.Household.ID, &current.Household.Name, &current.Household.Timezone, &current.Role, &expires, &lastSeen, &current.MembershipID, &current.User.EmailVerified, &current.User.Username)
	if err != nil {
		return nil, 0, database.Normalize(err)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT capability FROM identity_permissions WHERE user_id = ? AND household_id = ? ORDER BY capability", current.User.ID, current.Household.ID)
	if err != nil {
		return nil, 0, database.Normalize(err)
	}
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			rows.Close()
			return nil, 0, database.Normalize(err)
		}
		current.Permissions = append(current.Permissions, permission)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, database.Normalize(err)
	}
	current.ExpiresAt = time.Unix(expires, 0).UTC()
	return current, lastSeen, nil
}

func (s *sqliteRepository) readBackupMemberRecords(ctx context.Context, tx unitofwork.Transaction, household string, limit int) ([]BackupMember, error) {
	q, err := database.Within(s.db, tx)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT u.id,u.name,COALESCE(u.email,''),u.password_hash,m.role,mi.id,COALESCE(u.username,'u-'||lower(u.id)),u.assisted FROM identity_memberships m JOIN identity_users u ON u.id=m.user_id JOIN identity_member_ids mi ON mi.user_id=m.user_id AND mi.household_id=m.household_id WHERE m.household_id=? ORDER BY u.id`, household)
	if err != nil {
		return nil, err
	}
	var members []BackupMember
	for rows.Next() {
		var m BackupMember
		if err = rows.Scan(&m.ID, &m.Name, &m.Email, &m.PasswordHash, &m.Role, &m.MembershipID, &m.Username, &m.Assisted); err != nil {
			break
		}
		members = append(members, m)
		if len(members) > limit {
			rows.Close()
			return members, nil
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	return members, nil
}

func (s *sqliteRepository) readInvitations(ctx context.Context, household string) ([]invitation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+invitationColumns+` FROM identity_invitations i JOIN identity_households h ON h.id=i.household_id WHERE i.household_id=? ORDER BY i.created_at DESC,i.id LIMIT 100`, household)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []invitation{}
	for rows.Next() {
		i, err := readInvitation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (s *sqliteRepository) readMemberIDs(ctx context.Context, household string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM identity_memberships WHERE household_id=? ORDER BY role,user_id`, household)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *sqliteRepository) readPINDevices(ctx context.Context, user, household string) ([]map[string]any, error) {
	devices := []map[string]any{}
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,expires_at,failures>=5 FROM identity_pin_devices WHERE user_id=? AND household_id=? ORDER BY created_at DESC", user, household)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var expiry int64
		var locked bool
		if err = rows.Scan(&id, &name, &expiry, &locked); err != nil {
			return nil, err
		}
		devices = append(devices, map[string]any{"id": id, "name": name, "expiresAt": time.Unix(expiry, 0).UTC(), "locked": locked})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}

const invitationColumns = `i.id,i.email,i.role,i.permissions,i.household_id,i.issuer_id,i.issuer_member_id,i.expires_at,i.accepted_at,i.revoked_at,h.name`

func readInvitation(row interface{ Scan(...any) error }) (invitation, error) {
	var i invitation
	var permissions string
	err := row.Scan(&i.ID, &i.Email, &i.Role, &permissions, &i.household, &i.issuer, &i.issuerMember, &i.expires, &i.accepted, &i.revoked, &i.householdName)
	if err == nil {
		err = json.Unmarshal([]byte(permissions), &i.Permissions)
	}
	i.ExpiresAt = time.Unix(i.expires, 0).UTC()
	i.Status = "pending"
	if i.expires <= time.Now().Unix() {
		i.Status = "expired"
	}
	if i.revoked != 0 {
		i.Status = "revoked"
	}
	if i.accepted != 0 {
		i.Status = "accepted"
	}
	return i, err
}
