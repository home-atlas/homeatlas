// Package unitofwork defines the shared transaction boundary without a database driver.
package unitofwork

import "errors"

type Transaction interface {
	Commit() error
	Rollback() error
}

var ErrNotFound = errors.New("record not found")

// Mutation reports a conditional write without exposing a driver result.
type Mutation struct {
	Affected   int64
	CountError error
}
