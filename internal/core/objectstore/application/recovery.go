package application

import (
	"context"
	"errors"
)

// RecoverPending retries durable deletion intents before workers and HTTP start.
// Failed remote deletions remain queued for the next restart or storage move.
func (s *Store) RecoverPending(ctx context.Context, collections []Collection) error {
	tasks, err := s.repository.CleanupTasks(ctx)
	if err != nil {
		return err
	}
	var failures error
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		failures = errors.Join(failures, s.clean(ctx, task.Household, task.Obsolete, collections))
	}
	return failures
}
