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

type paymentIntentRepo struct{ db *sql.DB }

func NewPaymentIntentRepo(db *sql.DB) PaymentIntentRepository { return &paymentIntentRepo{db: db} }

const paymentIntentColumns = `id, invoice_id, method, status, amount, payer_user_id, created_at, processed_at`

func (r *paymentIntentRepo) Create(ctx context.Context, p *model.PaymentIntent) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	_, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		INSERT INTO payment_intents (`+paymentIntentColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		p.ID, p.InvoiceID, p.Method, p.Status, p.Amount,
		uuidPtrToNullString(p.PayerUserID), p.CreatedAt, timePtrToNullTime(p.ProcessedAt),
	)
	return err
}

func (r *paymentIntentRepo) FindByID(ctx context.Context, id uuid.UUID) (*model.PaymentIntent, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+paymentIntentColumns+` FROM payment_intents WHERE id = $1`, id)
	return scanPaymentIntent(row)
}

func (r *paymentIntentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, from, to constant.PaymentIntentStatus, processedAt *time.Time) error {
	res, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		UPDATE payment_intents
		SET status = $1, processed_at = $2
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

func (r *paymentIntentRepo) List(ctx context.Context, invoiceID *uuid.UUID, offset, limit int) ([]model.PaymentIntent, int64, error) {
	q := transaction.FromCtx(ctx, r.db)

	whereClause := ""
	args := []any{}
	if invoiceID != nil {
		whereClause = "WHERE invoice_id = $1"
		args = append(args, *invoiceID)
	}

	var total int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM payment_intents `+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	limitArg := len(args) - 1
	offsetArg := len(args)
	rows, err := q.QueryContext(ctx,
		`SELECT `+paymentIntentColumns+` FROM payment_intents `+whereClause+
			` ORDER BY created_at DESC LIMIT $`+itoa(limitArg)+` OFFSET $`+itoa(offsetArg),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]model.PaymentIntent, 0)
	for rows.Next() {
		p, err := scanPaymentIntentRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *paymentIntentRepo) FindLatestSuccessByInvoice(ctx context.Context, invoiceID uuid.UUID) (*model.PaymentIntent, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx, `
		SELECT `+paymentIntentColumns+` FROM payment_intents
		WHERE invoice_id = $1 AND status = $2
		ORDER BY processed_at DESC NULLS LAST
		LIMIT 1`,
		invoiceID, constant.PaymentSuccess,
	)
	return scanPaymentIntent(row)
}

func scanPaymentIntent(row *sql.Row) (*model.PaymentIntent, error) {
	var p model.PaymentIntent
	var payer sql.NullString
	var processed sql.NullTime
	err := row.Scan(&p.ID, &p.InvoiceID, &p.Method, &p.Status, &p.Amount, &payer, &p.CreatedAt, &processed)
	if err != nil {
		return nil, mapNoRowsToNotFound(err)
	}
	p.PayerUserID = nullStringToUUIDPtr(payer)
	p.ProcessedAt = nullTimePtr(processed)
	return &p, nil
}

func scanPaymentIntentRow(row *sql.Rows) (*model.PaymentIntent, error) {
	var p model.PaymentIntent
	var payer sql.NullString
	var processed sql.NullTime
	err := row.Scan(&p.ID, &p.InvoiceID, &p.Method, &p.Status, &p.Amount, &payer, &p.CreatedAt, &processed)
	if err != nil {
		return nil, err
	}
	p.PayerUserID = nullStringToUUIDPtr(payer)
	p.ProcessedAt = nullTimePtr(processed)
	return &p, nil
}
