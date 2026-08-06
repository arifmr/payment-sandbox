package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
)

// UserRepository abstracts user persistence.
type UserRepository interface {
	Create(ctx context.Context, u *model.User) error
	FindByEmail(ctx context.Context, email string) (*model.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*model.User, error)
}

// WalletRepository abstracts wallet persistence.
type WalletRepository interface {
	Create(ctx context.Context, w *model.Wallet) error
	FindByMerchantID(ctx context.Context, merchantID uuid.UUID) (*model.Wallet, error)
	// AddBalance atomically increments balance using optimistic locking.
	AddBalance(ctx context.Context, merchantID uuid.UUID, delta int64) error
}

// TopupRepository abstracts topup persistence.
type TopupRepository interface {
	Create(ctx context.Context, t *model.Topup) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.Topup, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status constant.TopupStatus, processedAt *time.Time) error
	List(ctx context.Context, merchantID *uuid.UUID, offset, limit int) ([]model.Topup, int64, error)
}

// InvoiceFilter is a filter applied to invoice listing.
type InvoiceFilter struct {
	MerchantID *uuid.UUID
	Status     *constant.InvoiceStatus
	From       *time.Time
	To         *time.Time
}

// InvoiceRepository abstracts invoice persistence.
type InvoiceRepository interface {
	Create(ctx context.Context, inv *model.Invoice) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.Invoice, error)
	// FindByIDForUpdate takes a row lock (SELECT ... FOR UPDATE) so callers can
	// read-then-write without a lost update. Only meaningful inside a transaction.
	FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*model.Invoice, error)
	FindByPaymentToken(ctx context.Context, token string) (*model.Invoice, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, from, to constant.InvoiceStatus, paidAt *time.Time) error
	List(ctx context.Context, f InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error)
	// MarkExpired sets PENDING invoices whose due_date < now to EXPIRED. Returns affected rows.
	MarkExpired(ctx context.Context, now time.Time) (int64, error)
}

// PaymentIntentFilter is a filter applied to payment intent listing.
type PaymentIntentFilter struct {
	InvoiceID *uuid.UUID
	Status    *constant.PaymentIntentStatus
}

// PaymentIntentRepository abstracts payment intent persistence.
type PaymentIntentRepository interface {
	Create(ctx context.Context, p *model.PaymentIntent) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.PaymentIntent, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, from, to constant.PaymentIntentStatus, processedAt *time.Time) error
	List(ctx context.Context, f PaymentIntentFilter, offset, limit int) ([]model.PaymentIntent, int64, error)
	FindLatestSuccessByInvoice(ctx context.Context, invoiceID uuid.UUID) (*model.PaymentIntent, error)
	// FailPendingForExpiredInvoices settles PENDING intents whose invoice has since
	// been EXPIRED, so they do not linger in a state they can never leave. Returns
	// affected rows.
	FailPendingForExpiredInvoices(ctx context.Context, now time.Time) (int64, error)
}

// RefreshTokenRepository abstracts refresh-token persistence.
type RefreshTokenRepository interface {
	Create(ctx context.Context, t *model.RefreshToken) error
	FindByHash(ctx context.Context, hash string) (*model.RefreshToken, error)
	Revoke(ctx context.Context, id uuid.UUID, at time.Time) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID, at time.Time) error
}

// RefundRepository abstracts refund persistence.
type RefundRepository interface {
	Create(ctx context.Context, r *model.Refund) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.Refund, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, from, to constant.RefundStatus, processedAt *time.Time) error
	ListByMerchant(ctx context.Context, merchantID uuid.UUID, offset, limit int) ([]model.Refund, int64, error)
	List(ctx context.Context, offset, limit int) ([]model.Refund, int64, error)
	// SumOutstandingByInvoice totals refunds for an invoice that are either already
	// settled (SUCCESS) or still in flight (REQUESTED/APPROVED). REJECTED and FAILED
	// release their reservation and are excluded.
	SumOutstandingByInvoice(ctx context.Context, invoiceID uuid.UUID) (int64, error)
}

// DashboardFilter for admin dashboard queries.
type DashboardFilter struct {
	MerchantID *uuid.UUID
	From       *time.Time
	To         *time.Time
}

// DashboardStats aggregates admin metrics.
type DashboardStats struct {
	TotalInvoices     int64 `json:"total_invoices"`
	TotalPaid         int64 `json:"total_paid"`
	TotalFailed       int64 `json:"total_failed"`
	TotalExpired      int64 `json:"total_expired"`
	TotalAmountPaid   int64 `json:"total_amount_paid"`
	TotalAmountRefund int64 `json:"total_amount_refund"`
}

// DashboardRepository computes admin metrics.
type DashboardRepository interface {
	Stats(ctx context.Context, f DashboardFilter) (*DashboardStats, error)
}
