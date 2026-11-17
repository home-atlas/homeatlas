package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/home-atlas/homeatlas/internal/core/unitofwork"
)

// Reader and Writer belong to the SQLite adapter; application ports expose domain records instead.
type Reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type Writer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type Connection interface {
	Reader
	Writer
}

func Within(db *sql.DB, tx unitofwork.Transaction) (Connection, error) {
	if tx == nil {
		return db, nil
	}
	return Transaction(tx)
}

func Normalize(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errors.Join(unitofwork.ErrNotFound, err)
	}
	return err
}
func Mutation(result sql.Result, err error) (unitofwork.Mutation, error) {
	if err != nil {
		return unitofwork.Mutation{}, Normalize(err)
	}
	count, countErr := result.RowsAffected()
	return unitofwork.Mutation{Affected: count, CountError: countErr}, nil
}

// Transaction unwraps only inside SQL adapters that implement transactional backup formats.
func Transaction(tx unitofwork.Transaction) (*sql.Tx, error) {
	if raw, ok := tx.(*sql.Tx); ok && raw != nil {
		return raw, nil
	}
	if wrapped, ok := tx.(interface{ SQLTransaction() *sql.Tx }); ok && wrapped.SQLTransaction() != nil {
		return wrapped.SQLTransaction(), nil
	}
	return nil, errors.New("invalid SQL transaction")
}
