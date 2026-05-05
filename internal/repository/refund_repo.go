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

type refundRepo struct{ db *sql.DB }

func NewRefundRepo(db *sql.DB) RefundRepository { return &refundRepo{db: db} }

const refundColumns = `id, invoice_id, payment_intent_id, merchant_id, amount, reason,
	status, created_at, updated_at, processed_at`

func (r *refundRepo) Create(ctx context.Context, rf *model.Refund) error {
	if rf.ID == uuid.Nil {
		rf.ID = uuid.New()
	}
	now := time.Now().UTC()
	if rf.CreatedAt.IsZero() {
		rf.CreatedAt = now
	}
	if rf.UpdatedAt.IsZero() {
		rf.UpdatedAt = now
	}
	_, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		INSERT INTO refunds (`+refundColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		rf.ID, rf.InvoiceID, rf.PaymentIntentID, rf.MerchantID, rf.Amount, rf.Reason,
		rf.Status, rf.CreatedAt, rf.UpdatedAt, timePtrToNullTime(rf.ProcessedAt),
	)
	return err
}

func (r *refundRepo) FindByID(ctx context.Context, id uuid.UUID) (*model.Refund, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+refundColumns+` FROM refunds WHERE id = $1`, id)
	return scanRefund(row)
}

func (r *refundRepo) UpdateStatus(ctx context.Context, id uuid.UUID, from, to constant.RefundStatus, processedAt *time.Time) error {
	res, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		UPDATE refunds
		SET status = $1, processed_at = $2, updated_at = NOW()
		WHERE id = $3 AND status = $4`,
		to, timePtrToNullTime(processedAt), id, from,
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

func (r *refundRepo) ListByMerchant(ctx context.Context, merchantID uuid.UUID, offset, limit int) ([]model.Refund, int64, error) {
	return r.list(ctx, "WHERE merchant_id = $1", []any{merchantID}, offset, limit)
}

func (r *refundRepo) List(ctx context.Context, offset, limit int) ([]model.Refund, int64, error) {
	return r.list(ctx, "", nil, offset, limit)
}

func (r *refundRepo) list(ctx context.Context, where string, args []any, offset, limit int) ([]model.Refund, int64, error) {
	q := transaction.FromCtx(ctx, r.db)

	var total int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM refunds `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	limitArg := len(args) - 1
	offsetArg := len(args)
	rows, err := q.QueryContext(ctx,
		`SELECT `+refundColumns+` FROM refunds `+where+
			` ORDER BY created_at DESC LIMIT $`+itoa(limitArg)+` OFFSET $`+itoa(offsetArg),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]model.Refund, 0)
	for rows.Next() {
		rf, err := scanRefundRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *rf)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func scanRefund(row *sql.Row) (*model.Refund, error) {
	var rf model.Refund
	var processed sql.NullTime
	err := row.Scan(
		&rf.ID, &rf.InvoiceID, &rf.PaymentIntentID, &rf.MerchantID, &rf.Amount, &rf.Reason,
		&rf.Status, &rf.CreatedAt, &rf.UpdatedAt, &processed,
	)
	if err != nil {
		return nil, mapNoRowsToNotFound(err)
	}
	rf.ProcessedAt = nullTimePtr(processed)
	return &rf, nil
}

func scanRefundRow(row *sql.Rows) (*model.Refund, error) {
	var rf model.Refund
	var processed sql.NullTime
	err := row.Scan(
		&rf.ID, &rf.InvoiceID, &rf.PaymentIntentID, &rf.MerchantID, &rf.Amount, &rf.Reason,
		&rf.Status, &rf.CreatedAt, &rf.UpdatedAt, &processed,
	)
	if err != nil {
		return nil, err
	}
	rf.ProcessedAt = nullTimePtr(processed)
	return &rf, nil
}
