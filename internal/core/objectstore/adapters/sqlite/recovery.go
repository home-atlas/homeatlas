package sqlite

import (
	"context"
	"github.com/home-atlas/homeatlas/internal/core/objectstore/domain"
)

// LocalKeys includes every household and pending source deletion, independently
// of module activation. A missing schema must never be treated as an empty one.
func (s *Repository) LocalKeys(ctx context.Context, c Collection) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT file_key FROM `+c.Table+` d WHERE file_key!='' AND NOT EXISTS(SELECT 1 FROM file_storage_locations l WHERE l.module_id=? AND l.file_key=d.file_key)
 UNION SELECT file_key FROM file_storage_cleanup WHERE module_id=? AND profile_id=''`, c.ID, c.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys[key] = true
	}
	return keys, rows.Err()
}

func (s *Repository) StageRemote(ctx context.Context, house string, p domain.Location) error {
	result, err := s.db.ExecContext(ctx, "INSERT INTO file_storage_cleanup SELECT ?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM file_storage_locations WHERE module_id=? AND file_key=?)", domain.PendingID(p.Module, p.Key), house, p.Module, p.Key, p.Profile, p.Object, p.Module, p.Key)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return domain.Conflict
	}
	return err
}

func (s *Repository) RemoteReferenced(ctx context.Context, profile, object string) (bool, error) {
	var yes bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM file_storage_locations WHERE profile_id=? AND object_key=?)", profile, object).Scan(&yes)
	return yes, err
}

func (s *Repository) CleanupTasks(ctx context.Context) ([]domain.CleanupTask, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT household_id,id,module_id,file_key,profile_id,object_key FROM file_storage_cleanup ORDER BY id LIMIT 256")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []domain.CleanupTask
	for rows.Next() {
		var v domain.CleanupTask
		if err := rows.Scan(&v.Household, &v.ID, &v.Module, &v.Key, &v.Profile, &v.Object); err != nil {
			return nil, err
		}
		tasks = append(tasks, v)
	}
	return tasks, rows.Err()
}
