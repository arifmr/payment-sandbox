package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

type invoiceRepo struct{ db *sql.DB }

func NewInvoiceRepo(db *sql.DB) InvoiceRepository { return &invoiceRepo{db: db} }

const invoiceColumns = `id, invoice_number, merchant_id, customer_name, customer_email, description,
	amount, status, due_date, payment_token, created_at, updated_at, paid_at`

func (r *invoiceRepo) Create(ctx context.Context, inv *model.Invoice) error {
	if inv.ID == uuid.Nil {
		inv.ID = uuid.New()
	}
	now := time.Now().UTC()
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = now
	}
	if inv.UpdatedAt.IsZero() {
		inv.UpdatedAt = now
	}
	_, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		INSERT INTO invoices (`+invoiceColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		inv.ID, inv.InvoiceNumber, inv.MerchantID, inv.CustomerName, inv.CustomerEmail, inv.Description,
		inv.Amount, inv.Status, inv.DueDate, inv.PaymentToken, inv.CreatedAt, inv.UpdatedAt,
		timePtrToNullTime(inv.PaidAt),
	)
	return mapWriteError(err)
}

func (r *invoiceRepo) FindByID(ctx context.Context, id uuid.UUID) (*model.Invoice, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE id = $1`, id)
	return scanInvoice(row)
}

func (r *invoiceRepo) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*model.Invoice, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE id = $1 FOR UPDATE`, id)
	return scanInvoice(row)
}

func (r *invoiceRepo) FindByPaymentToken(ctx context.Context, tokenStr string) (*model.Invoice, error) {
	row := transaction.FromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE payment_token = $1`, tokenStr)
	return scanInvoice(row)
}

// UpdateStatus uses CAS-style WHERE to enforce the transition. Returns ErrInvalidState if no row matched.
func (r *invoiceRepo) UpdateStatus(ctx context.Context, id uuid.UUID, from, to constant.InvoiceStatus, paidAt *time.Time) error {
	res, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		UPDATE invoices
		SET status = $1, paid_at = $2, updated_at = NOW()
		WHERE id = $3 AND status = $4`,
		to, timePtrToNullTime(paidAt), id, from,
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

func (r *invoiceRepo) List(ctx context.Context, f InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error) {
	q := transaction.FromCtx(ctx, r.db)

	conds := []string{}
	args := []any{}
	if f.MerchantID != nil {
		args = append(args, *f.MerchantID)
		conds = append(conds, "merchant_id = $"+itoa(len(args)))
	}
	if f.Status != nil {
		args = append(args, *f.Status)
		conds = append(conds, "status = $"+itoa(len(args)))
	}
	if f.From != nil {
		args = append(args, *f.From)
		conds = append(conds, "created_at >= $"+itoa(len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		conds = append(conds, "created_at <= $"+itoa(len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	var total int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	limitArg := len(args) - 1
	offsetArg := len(args)
	rows, err := q.QueryContext(ctx,
		`SELECT `+invoiceColumns+` FROM invoices `+where+
			` ORDER BY created_at DESC LIMIT $`+itoa(limitArg)+` OFFSET $`+itoa(offsetArg),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]model.Invoice, 0)
	for rows.Next() {
		inv, err := scanInvoiceRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *inv)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *invoiceRepo) MarkExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := transaction.FromCtx(ctx, r.db).ExecContext(ctx, `
		UPDATE invoices
		SET status = $1, updated_at = NOW()
		WHERE status = $2 AND due_date < $3`,
		constant.InvoiceExpired, constant.InvoicePending, now,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanInvoice(row *sql.Row) (*model.Invoice, error) {
	var inv model.Invoice
	var paidAt sql.NullTime
	err := row.Scan(
		&inv.ID, &inv.InvoiceNumber, &inv.MerchantID, &inv.CustomerName, &inv.CustomerEmail, &inv.Description,
		&inv.Amount, &inv.Status, &inv.DueDate, &inv.PaymentToken, &inv.CreatedAt, &inv.UpdatedAt, &paidAt,
	)
	if err != nil {
		return nil, mapNoRowsToNotFound(err)
	}
	inv.PaidAt = nullTimePtr(paidAt)
	return &inv, nil
}

func scanInvoiceRow(row *sql.Rows) (*model.Invoice, error) {
	var inv model.Invoice
	var paidAt sql.NullTime
	err := row.Scan(
		&inv.ID, &inv.InvoiceNumber, &inv.MerchantID, &inv.CustomerName, &inv.CustomerEmail, &inv.Description,
		&inv.Amount, &inv.Status, &inv.DueDate, &inv.PaymentToken, &inv.CreatedAt, &inv.UpdatedAt, &paidAt,
	)
	if err != nil {
		return nil, err
	}
	inv.PaidAt = nullTimePtr(paidAt)
	return &inv, nil
}
