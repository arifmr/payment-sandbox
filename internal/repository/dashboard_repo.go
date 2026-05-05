package repository

import (
	"context"
	"database/sql"
	"strings"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

type dashboardRepo struct{ db *sql.DB }

func NewDashboardRepo(db *sql.DB) DashboardRepository { return &dashboardRepo{db: db} }

func (r *dashboardRepo) Stats(ctx context.Context, f DashboardFilter) (*DashboardStats, error) {
	q := transaction.FromCtx(ctx, r.db)
	out := &DashboardStats{}

	// --- Invoice aggregates (total / paid / expired / total amount paid) ---
	invConds := []string{}
	invArgs := []any{}
	if f.MerchantID != nil {
		invArgs = append(invArgs, *f.MerchantID)
		invConds = append(invConds, "merchant_id = $"+itoa(len(invArgs)))
	}
	if f.From != nil {
		invArgs = append(invArgs, *f.From)
		invConds = append(invConds, "created_at >= $"+itoa(len(invArgs)))
	}
	if f.To != nil {
		invArgs = append(invArgs, *f.To)
		invConds = append(invConds, "created_at <= $"+itoa(len(invArgs)))
	}
	invWhere := ""
	if len(invConds) > 0 {
		invWhere = "WHERE " + strings.Join(invConds, " AND ")
	}

	err := q.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = '`+string(constant.InvoicePaid)+`'),
			COUNT(*) FILTER (WHERE status = '`+string(constant.InvoiceExpired)+`'),
			COALESCE(SUM(amount) FILTER (WHERE status = '`+string(constant.InvoicePaid)+`'), 0)
		FROM invoices `+invWhere,
		invArgs...,
	).Scan(&out.TotalInvoices, &out.TotalPaid, &out.TotalExpired, &out.TotalAmountPaid)
	if err != nil {
		return nil, err
	}

	// --- Failed payment intents (joined with invoices for merchant filter) ---
	piConds := []string{"payment_intents.status = $1"}
	piArgs := []any{constant.PaymentFailed}
	if f.MerchantID != nil {
		piArgs = append(piArgs, *f.MerchantID)
		piConds = append(piConds, "invoices.merchant_id = $"+itoa(len(piArgs)))
	}
	if f.From != nil {
		piArgs = append(piArgs, *f.From)
		piConds = append(piConds, "payment_intents.created_at >= $"+itoa(len(piArgs)))
	}
	if f.To != nil {
		piArgs = append(piArgs, *f.To)
		piConds = append(piConds, "payment_intents.created_at <= $"+itoa(len(piArgs)))
	}
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM payment_intents
		JOIN invoices ON invoices.id = payment_intents.invoice_id
		WHERE `+strings.Join(piConds, " AND "),
		piArgs...,
	).Scan(&out.TotalFailed); err != nil {
		return nil, err
	}

	// --- Total refund amount (SUCCESS only) ---
	rfConds := []string{"status = $1"}
	rfArgs := []any{constant.RefundSuccess}
	if f.MerchantID != nil {
		rfArgs = append(rfArgs, *f.MerchantID)
		rfConds = append(rfConds, "merchant_id = $"+itoa(len(rfArgs)))
	}
	if f.From != nil {
		rfArgs = append(rfArgs, *f.From)
		rfConds = append(rfConds, "created_at >= $"+itoa(len(rfArgs)))
	}
	if f.To != nil {
		rfArgs = append(rfArgs, *f.To)
		rfConds = append(rfConds, "created_at <= $"+itoa(len(rfArgs)))
	}
	if err := q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0) FROM refunds WHERE `+strings.Join(rfConds, " AND "),
		rfArgs...,
	).Scan(&out.TotalAmountRefund); err != nil {
		return nil, err
	}

	return out, nil
}
