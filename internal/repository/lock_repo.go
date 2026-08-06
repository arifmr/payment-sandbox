package repository

import (
	"context"
	"database/sql"

	"github.com/dboarif/payment-sandbox/internal/transaction"
)

// AdvisoryLockKey identifies a named cluster-wide lock. Keys are arbitrary but must be
// stable across deploys, so they are declared here rather than passed as literals.
type AdvisoryLockKey int64

const (
	// LockInvoiceExpirySweep serialises the background expiry sweeper so that N
	// replicas do not all run the same bulk UPDATE on every tick.
	LockInvoiceExpirySweep AdvisoryLockKey = 8_642_001
)

// AdvisoryLocker takes cluster-wide advisory locks.
type AdvisoryLocker interface {
	// TryLockTx attempts to take a transaction-scoped advisory lock without blocking.
	// It reports whether the lock was acquired. Must be called inside a transaction.
	TryLockTx(ctx context.Context, key AdvisoryLockKey) (bool, error)
}

type advisoryLocker struct{ db *sql.DB }

func NewAdvisoryLocker(db *sql.DB) AdvisoryLocker { return &advisoryLocker{db: db} }

// TryLockTx uses pg_try_advisory_xact_lock, not pg_try_advisory_lock.
//
// The distinction matters with a connection pool. A session-scoped advisory lock is
// bound to the connection that took it, and pg_advisory_unlock would have to run on
// that same connection — but database/sql hands out whichever connection is free, so
// the unlock can land elsewhere and leak the lock permanently. The transaction-scoped
// variant is released automatically on COMMIT or ROLLBACK, which the Unit of Work
// already guarantees.
func (l *advisoryLocker) TryLockTx(ctx context.Context, key AdvisoryLockKey) (bool, error) {
	var acquired bool
	err := transaction.FromCtx(ctx, l.db).
		QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, int64(key)).
		Scan(&acquired)
	if err != nil {
		return false, err
	}
	return acquired, nil
}
