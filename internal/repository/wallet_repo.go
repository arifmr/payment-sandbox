package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

type walletRepo struct{ db *sql.DB }

func NewWalletRepo(db *sql.DB) WalletRepository { return &walletRepo{db: db} }

const walletColumns = `id, merchant_id, balance, version, updated_at`

func (r *walletRepo) Create(ctx context.Context, w *model.Wallet) error {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	if w.UpdatedAt.IsZero() {
		w.UpdatedAt = time.Now().UTC()
	}
	_, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		INSERT INTO wallets (`+walletColumns+`)
		VALUES ($1, $2, $3, $4, $5)`,
		w.ID, w.MerchantID, w.Balance, w.Version, w.UpdatedAt,
	)
	return err
}

func (r *walletRepo) FindByMerchantID(ctx context.Context, merchantID uuid.UUID) (*model.Wallet, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+walletColumns+` FROM wallets WHERE merchant_id = $1`, merchantID)
	var w model.Wallet
	err := row.Scan(&w.ID, &w.MerchantID, &w.Balance, &w.Version, &w.UpdatedAt)
	if err != nil {
		return nil, mapNoRowsToNotFound(err)
	}
	return &w, nil
}

// AddBalance applies +/- delta atomically. For negative deltas the resulting balance
// is guarded to be non-negative by including (balance + delta >= 0) in the WHERE clause;
// when no row matches we distinguish missing wallet from insufficient funds.
func (r *walletRepo) AddBalance(ctx context.Context, merchantID uuid.UUID, delta int64) error {
	q := transaction.FromCtx(ctx, r.db)

	var query string
	var args []any
	if delta < 0 {
		query = `
			UPDATE wallets
			SET balance = balance + $1, version = version + 1, updated_at = NOW()
			WHERE merchant_id = $2 AND balance + $1 >= 0`
		args = []any{delta, merchantID}
	} else {
		query = `
			UPDATE wallets
			SET balance = balance + $1, version = version + 1, updated_at = NOW()
			WHERE merchant_id = $2`
		args = []any{delta, merchantID}
	}

	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// No row updated. Check whether the wallet exists to give a precise error.
		var exists int
		err := q.QueryRowContext(ctx, `SELECT 1 FROM wallets WHERE merchant_id = $1`, merchantID).Scan(&exists)
		if err != nil {
			if err == sql.ErrNoRows {
				return apperror.ErrNotFound
			}
			return err
		}
		// Wallet exists but the WHERE failed → must be insufficient funds.
		return apperror.ErrInsufficientFunds
	}
	return nil
}
