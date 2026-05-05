package transaction

import (
	"context"
	"database/sql"
)

// Querier is the subset of *sql.DB / *sql.Tx that repositories need.
// Both types satisfy this interface, so a repository can use a single
// connection abstraction whether it is operating standalone or inside a tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type txKey struct{}

// WithTx attaches an active tx to context. Used by UnitOfWork.
func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// FromCtx returns the tx-bound Querier if a transaction is in flight,
// otherwise the provided fallback. Repositories should call this once per query.
func FromCtx(ctx context.Context, fallback Querier) Querier {
	if v := ctx.Value(txKey{}); v != nil {
		if tx, ok := v.(*sql.Tx); ok && tx != nil {
			return tx
		}
	}
	return fallback
}

// UnitOfWork executes a function inside a single DB transaction.
// The function should perform all repository calls using the supplied ctx,
// which carries the bound tx.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type sqlUoW struct {
	db *sql.DB
}

func NewUoW(db *sql.DB) UnitOfWork { return &sqlUoW{db: db} }

func (u *sqlUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(WithTx(ctx, tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
