package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/pkg/token"
	"github.com/dboarif/payment-sandbox/internal/repository"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

// createInvoiceMaxAttempts bounds the retry loop that regenerates invoice_number
// and payment_token when the DB rejects them as duplicates.
const createInvoiceMaxAttempts = 3

type CreateInvoiceInput struct {
	MerchantID    uuid.UUID
	CustomerName  string
	CustomerEmail string
	Description   string
	Amount        int64
	DueDate       time.Time
}

// ExpiryResult reports what one sweep did. Skipped is true when another replica held
// the advisory lock, which is a normal outcome rather than an error.
type ExpiryResult struct {
	InvoicesExpired int64
	IntentsFailed   int64
	Skipped         bool
}

type InvoiceService interface {
	Create(ctx context.Context, in CreateInvoiceInput) (*model.Invoice, error)
	GetByID(ctx context.Context, id uuid.UUID, merchantID uuid.UUID) (*model.Invoice, error)
	GetByPaymentToken(ctx context.Context, paymentToken string) (*model.Invoice, error)
	List(ctx context.Context, f repository.InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error)
	ExpireDue(ctx context.Context) (ExpiryResult, error)
}

type invoiceService struct {
	invoices repository.InvoiceRepository
	intents  repository.PaymentIntentRepository
	locker   repository.AdvisoryLocker
	uow      transaction.UnitOfWork
}

func NewInvoiceService(
	i repository.InvoiceRepository,
	pi repository.PaymentIntentRepository,
	locker repository.AdvisoryLocker,
	uow transaction.UnitOfWork,
) InvoiceService {
	return &invoiceService{invoices: i, intents: pi, locker: locker, uow: uow}
}

func (s *invoiceService) Create(ctx context.Context, in CreateInvoiceInput) (*model.Invoice, error) {
	if in.Amount <= 0 {
		return nil, apperror.ErrInvalidAmount
	}
	if in.DueDate.Before(time.Now()) {
		return nil, apperror.New(apperror.KindBadRequest, "INVALID_DUE_DATE", "due_date must be in the future")
	}
	// invoice_number and payment_token are randomly generated against a UNIQUE
	// constraint. A collision is extremely unlikely but must not surface as a 500,
	// so regenerate and retry a bounded number of times.
	var lastErr error
	for attempt := 0; attempt < createInvoiceMaxAttempts; attempt++ {
		num, err := token.InvoiceNumber()
		if err != nil {
			return nil, err
		}
		tok, err := token.Random(32)
		if err != nil {
			return nil, err
		}
		inv := &model.Invoice{
			ID:            uuid.New(),
			InvoiceNumber: num,
			MerchantID:    in.MerchantID,
			CustomerName:  in.CustomerName,
			CustomerEmail: in.CustomerEmail,
			Description:   in.Description,
			Amount:        in.Amount,
			Status:        constant.InvoicePending,
			DueDate:       in.DueDate,
			PaymentToken:  tok,
		}
		err = s.invoices.Create(ctx, inv)
		if err == nil {
			return inv, nil
		}
		if !apperror.IsKind(err, apperror.KindConflict) {
			return nil, err
		}
		lastErr = err
	}
	return nil, apperror.Wrap(apperror.KindInternal, "INVOICE_NUMBER_COLLISION",
		"could not allocate a unique invoice number", lastErr)
}

func (s *invoiceService) GetByID(ctx context.Context, id uuid.UUID, merchantID uuid.UUID) (*model.Invoice, error) {
	inv, err := s.invoices.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if inv.MerchantID != merchantID {
		return nil, apperror.ErrForbidden
	}
	return inv, nil
}

func (s *invoiceService) GetByPaymentToken(ctx context.Context, paymentToken string) (*model.Invoice, error) {
	return s.invoices.FindByPaymentToken(ctx, paymentToken)
}

func (s *invoiceService) List(ctx context.Context, f repository.InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error) {
	return s.invoices.List(ctx, f, offset, limit)
}

// ExpireDue moves overdue PENDING invoices to EXPIRED and settles the payment intents
// left behind by them.
//
// Both steps run in one transaction guarded by a cluster-wide advisory lock. The lock is
// there because the sweeper is an in-process goroutine: with N replicas, N sweepers fire
// every tick. MarkExpired is idempotent so duplicates cause no data damage, but they are
// wasted work and — on a large invoices table — N concurrent bulk UPDATEs contend for row
// locks. Failing to acquire the lock means another replica is already sweeping, so this
// tick is skipped rather than treated as an error.
//
// Expiring the intents matters for reporting: an intent whose invoice became EXPIRED can
// never legally reach SUCCESS (InvoiceFSM forbids EXPIRED -> PAID), so leaving it PENDING
// would understate total_failed on the §2.6 dashboard forever.
func (s *invoiceService) ExpireDue(ctx context.Context) (ExpiryResult, error) {
	var out ExpiryResult

	err := s.uow.Do(ctx, func(ctx context.Context) error {
		acquired, err := s.locker.TryLockTx(ctx, repository.LockInvoiceExpirySweep)
		if err != nil {
			return err
		}
		if !acquired {
			out.Skipped = true
			return nil
		}

		now := time.Now().UTC()
		invoices, err := s.invoices.MarkExpired(ctx, now)
		if err != nil {
			return err
		}
		// Ordered deliberately: the intents query looks for invoices already EXPIRED,
		// so it must run after MarkExpired to see this sweep's work.
		intents, err := s.intents.FailPendingForExpiredInvoices(ctx, now)
		if err != nil {
			return err
		}

		out.InvoicesExpired = invoices
		out.IntentsFailed = intents
		return nil
	})
	if err != nil {
		return ExpiryResult{}, err
	}
	return out, nil
}
