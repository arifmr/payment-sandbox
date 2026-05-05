package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

type topupRepo struct{ db *sql.DB }

func NewTopupRepo(db *sql.DB) TopupRepository { return &topupRepo{db: db} }

const topupColumns = `id, merchant_id, amount, status, created_at, processed_at`

func (r *topupRepo) Create(ctx context.Context, t *model.Topup) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	_, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		INSERT INTO topups (`+topupColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.MerchantID, t.Amount, t.Status, t.CreatedAt, timePtrToNullTime(t.ProcessedAt),
	)
	return err
}

func (r *topupRepo) FindByID(ctx context.Context, id uuid.UUID) (*model.Topup, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+topupColumns+` FROM topups WHERE id = $1`, id)
	var t model.Topup
	var processedAt sql.NullTime
	err := row.Scan(&t.ID, &t.MerchantID, &t.Amount, &t.Status, &t.CreatedAt, &processedAt)
	if err != nil {
		return nil, mapNoRowsToNotFound(err)
	}
	t.ProcessedAt = nullTimePtr(processedAt)
	return &t, nil
}

// UpdateStatus enforces transition by including current status (PENDING) in the WHERE clause.
// Returns ErrInvalidState if no rows updated.
func (r *topupRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status constant.TopupStatus, processedAt *time.Time) error {
	res, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		UPDATE topups
		SET status = $1, processed_at = $2
		WHERE id = $3 AND status = $4`,
		status, timePtrToNullTime(processedAt), id, constant.TopupPending,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperror.ErrInvalidState
	}
	return nil
}

func (r *topupRepo) List(ctx context.Context, merchantID *uuid.UUID, offset, limit int) ([]model.Topup, int64, error) {
	q := transaction.FromCtx(ctx, r.db)

	whereClause := ""
	var args []any
	if merchantID != nil {
		whereClause = "WHERE merchant_id = $1"
		args = append(args, *merchantID)
	}

	var total int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM topups `+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	limitArg := len(args) - 1
	offsetArg := len(args)
	rows, err := q.QueryContext(ctx,
		`SELECT `+topupColumns+` FROM topups `+whereClause+
			` ORDER BY created_at DESC LIMIT $`+itoa(limitArg)+` OFFSET $`+itoa(offsetArg),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]model.Topup, 0)
	for rows.Next() {
		var t model.Topup
		var processedAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.MerchantID, &t.Amount, &t.Status, &t.CreatedAt, &processedAt); err != nil {
			return nil, 0, err
		}
		t.ProcessedAt = nullTimePtr(processedAt)
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
