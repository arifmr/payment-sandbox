package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/repository"
)

// ── mockPaymentIntentRepo ─────────────────────────────────────────────────────

type mockPaymentIntentRepo struct {
	store         map[uuid.UUID]*model.PaymentIntent
	latestSuccess map[uuid.UUID]*model.PaymentIntent // keyed by invoiceID
	// invoices, when wired, lets FailPendingForExpiredInvoices read live invoice
	// status — mirroring the real query's EXISTS subquery against the invoices table
	// rather than a snapshot taken earlier.
	invoices *mockInvoiceRepo
}

func newMockPaymentIntentRepo() *mockPaymentIntentRepo {
	return &mockPaymentIntentRepo{
		store:         map[uuid.UUID]*model.PaymentIntent{},
		latestSuccess: map[uuid.UUID]*model.PaymentIntent{},
	}
}

func (m *mockPaymentIntentRepo) Create(_ context.Context, p *model.PaymentIntent) error {
	p.CreatedAt = time.Now()
	m.store[p.ID] = p
	return nil
}

func (m *mockPaymentIntentRepo) FindByID(_ context.Context, id uuid.UUID) (*model.PaymentIntent, error) {
	p, ok := m.store[id]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return p, nil
}

func (m *mockPaymentIntentRepo) UpdateStatus(_ context.Context, id uuid.UUID, from, to constant.PaymentIntentStatus, processedAt *time.Time) error {
	p, ok := m.store[id]
	if !ok {
		return apperror.ErrNotFound
	}
	if p.Status != from {
		return apperror.ErrInvalidState
	}
	p.Status = to
	if processedAt != nil {
		p.ProcessedAt = processedAt
	}
	if to == constant.PaymentSuccess {
		m.latestSuccess[p.InvoiceID] = p
	}
	return nil
}

func (m *mockPaymentIntentRepo) List(_ context.Context, f repository.PaymentIntentFilter, offset, limit int) ([]model.PaymentIntent, int64, error) {
	result := make([]model.PaymentIntent, 0)
	for _, p := range m.store {
		if f.InvoiceID != nil && p.InvoiceID != *f.InvoiceID {
			continue
		}
		if f.Status != nil && p.Status != *f.Status {
			continue
		}
		result = append(result, *p)
	}
	return result, int64(len(result)), nil
}

func (m *mockPaymentIntentRepo) FindLatestSuccessByInvoice(_ context.Context, invoiceID uuid.UUID) (*model.PaymentIntent, error) {
	p, ok := m.latestSuccess[invoiceID]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return p, nil
}

// FailPendingForExpiredInvoices mirrors the real UPDATE: PENDING intents whose invoice
// is EXPIRED get settled as FAILED. Invoice status is read live from the invoice repo,
// so a test can prove the service runs this *after* MarkExpired.
func (m *mockPaymentIntentRepo) FailPendingForExpiredInvoices(_ context.Context, now time.Time) (int64, error) {
	if m.invoices == nil {
		return 0, nil // no invoice table wired: nothing can be matched
	}
	var n int64
	for _, p := range m.store {
		if p.Status != constant.PaymentPending {
			continue
		}
		inv, ok := m.invoices.store[p.InvoiceID]
		if !ok || inv.Status != constant.InvoiceExpired {
			continue
		}
		p.Status = constant.PaymentFailed
		processed := now
		p.ProcessedAt = &processed
		n++
	}
	return n, nil
}

var _ repository.PaymentIntentRepository = (*mockPaymentIntentRepo)(nil)

// ── helpers ───────────────────────────────────────────────────────────────────

func newPaymentSvc(invRepo *mockInvoiceRepo, piRepo *mockPaymentIntentRepo, wRepo *mockWalletRepo) PaymentService {
	return NewPaymentService(invRepo, piRepo, wRepo, noopUoW{})
}

// seedPendingInvoice inserts a PENDING invoice with a future due date into the repo and returns it.
func seedPendingInvoice(repo *mockInvoiceRepo, merchantID uuid.UUID, amount int64) *model.Invoice {
	inv := &model.Invoice{
		ID:           uuid.New(),
		MerchantID:   merchantID,
		Amount:       amount,
		Status:       constant.InvoicePending,
		DueDate:      time.Now().Add(24 * time.Hour),
		PaymentToken: uuid.NewString(),
	}
	repo.store[inv.ID] = inv
	repo.tokenIndex[inv.PaymentToken] = inv
	return inv
}

// ── CreateIntent ──────────────────────────────────────────────────────────────

func TestPaymentService_CreateIntent_HappyPath_Wallet(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)

	inv := seedPendingInvoice(invRepo, uuid.New(), 3000)

	pi, err := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodWallet, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pi.Status != constant.PaymentPending {
		t.Fatalf("want status PENDING, got %s", pi.Status)
	}
	if pi.Method != constant.PaymentMethodWallet {
		t.Fatalf("want method WALLET, got %s", pi.Method)
	}
	if pi.Amount != 3000 {
		t.Fatalf("want amount 3000, got %d", pi.Amount)
	}
}

func TestPaymentService_CreateIntent_HappyPath_Dummy(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	svc := newPaymentSvc(invRepo, piRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	for _, method := range []constant.PaymentMethod{constant.PaymentMethodVADummy, constant.PaymentMethodEwalletDummy} {
		inv := seedPendingInvoice(invRepo, uuid.New(), 1000)
		pi, err := svc.CreateIntent(context.Background(), inv.PaymentToken, method, nil)
		if err != nil {
			t.Fatalf("CreateIntent(%s): unexpected error: %v", method, err)
		}
		if pi.Method != method {
			t.Fatalf("want method %s, got %s", method, pi.Method)
		}
	}
}

func TestPaymentService_CreateIntent_InvalidMethod(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	svc := newPaymentSvc(invRepo, newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})
	inv := seedPendingInvoice(invRepo, uuid.New(), 1000)

	_, err := svc.CreateIntent(context.Background(), inv.PaymentToken, "UNKNOWN_METHOD", nil)
	if err == nil {
		t.Fatal("want error for invalid method, got nil")
	}
}

func TestPaymentService_CreateIntent_InvoiceNotFound(t *testing.T) {
	svc := newPaymentSvc(newMockInvoiceRepo(), newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	_, err := svc.CreateIntent(context.Background(), "nonexistent-token", constant.PaymentMethodWallet, nil)
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPaymentService_CreateIntent_InvoicePaid(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	svc := newPaymentSvc(invRepo, newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	inv := seedPendingInvoice(invRepo, uuid.New(), 1000)
	inv.Status = constant.InvoicePaid // mark as paid directly

	_, err := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodWallet, nil)
	if !errors.Is(err, apperror.ErrInvoiceNotPayable) {
		t.Fatalf("want ErrInvoiceNotPayable, got %v", err)
	}
}

func TestPaymentService_CreateIntent_InvoiceExpiredStatus(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	svc := newPaymentSvc(invRepo, newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	inv := seedPendingInvoice(invRepo, uuid.New(), 1000)
	inv.Status = constant.InvoiceExpired // mark as expired directly

	_, err := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodWallet, nil)
	if !errors.Is(err, apperror.ErrInvoiceExpired) {
		t.Fatalf("want ErrInvoiceExpired, got %v", err)
	}
}

func TestPaymentService_CreateIntent_DueDatePassed(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	svc := newPaymentSvc(invRepo, newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	// PENDING invoice but due date already passed.
	inv := &model.Invoice{
		ID:           uuid.New(),
		MerchantID:   uuid.New(),
		Amount:       1000,
		Status:       constant.InvoicePending,
		DueDate:      time.Now().Add(-time.Hour),
		PaymentToken: uuid.NewString(),
	}
	invRepo.store[inv.ID] = inv
	invRepo.tokenIndex[inv.PaymentToken] = inv

	_, err := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodWallet, nil)
	if !errors.Is(err, apperror.ErrInvoiceExpired) {
		t.Fatalf("want ErrInvoiceExpired for past due date, got %v", err)
	}
}

// ── Process ───────────────────────────────────────────────────────────────────

func TestPaymentService_Process_Success_Wallet_WithPayer(t *testing.T) {
	merchantID := uuid.New()
	payerID := uuid.New()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0, payerID: 5000}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)

	inv := seedPendingInvoice(invRepo, merchantID, 2000)
	pi, _ := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodWallet, &payerID)

	result, err := svc.Process(context.Background(), pi.ID, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != constant.PaymentSuccess {
		t.Fatalf("want PaymentSuccess, got %s", result.Status)
	}
	if result.ProcessedAt == nil {
		t.Fatal("ProcessedAt must be set")
	}
	if invRepo.store[inv.ID].Status != constant.InvoicePaid {
		t.Fatalf("invoice must be PAID, got %s", invRepo.store[inv.ID].Status)
	}
	if wRepo.balances[merchantID] != 2000 {
		t.Fatalf("merchant balance want 2000, got %d", wRepo.balances[merchantID])
	}
	if wRepo.balances[payerID] != 3000 {
		t.Fatalf("payer balance want 3000, got %d", wRepo.balances[payerID])
	}
}

func TestPaymentService_Process_Success_Wallet_NoPayer(t *testing.T) {
	merchantID := uuid.New()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)

	inv := seedPendingInvoice(invRepo, merchantID, 1500)
	pi, _ := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodWallet, nil)

	_, err := svc.Process(context.Background(), pi.ID, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wRepo.balances[merchantID] != 1500 {
		t.Fatalf("merchant balance want 1500, got %d", wRepo.balances[merchantID])
	}
}

func TestPaymentService_Process_Success_Dummy(t *testing.T) {
	merchantID := uuid.New()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)

	inv := seedPendingInvoice(invRepo, merchantID, 800)
	pi, _ := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodVADummy, nil)

	_, err := svc.Process(context.Background(), pi.ID, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wRepo.balances[merchantID] != 800 {
		t.Fatalf("merchant balance want 800, got %d", wRepo.balances[merchantID])
	}
	if invRepo.store[inv.ID].Status != constant.InvoicePaid {
		t.Fatalf("invoice must be PAID, got %s", invRepo.store[inv.ID].Status)
	}
}

func TestPaymentService_Process_Failed(t *testing.T) {
	merchantID := uuid.New()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)

	inv := seedPendingInvoice(invRepo, merchantID, 1000)
	pi, _ := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodWallet, nil)

	result, err := svc.Process(context.Background(), pi.ID, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != constant.PaymentFailed {
		t.Fatalf("want PaymentFailed, got %s", result.Status)
	}
	if invRepo.store[inv.ID].Status != constant.InvoicePending {
		t.Fatalf("invoice must remain PENDING on failure, got %s", invRepo.store[inv.ID].Status)
	}
	if wRepo.balances[merchantID] != 0 {
		t.Fatalf("wallet must not be credited on failure, got %d", wRepo.balances[merchantID])
	}
}

func TestPaymentService_Process_NotFound(t *testing.T) {
	svc := newPaymentSvc(newMockInvoiceRepo(), newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	_, err := svc.Process(context.Background(), uuid.New(), true)
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPaymentService_Process_InvalidState(t *testing.T) {
	merchantID := uuid.New()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)

	inv := seedPendingInvoice(invRepo, merchantID, 1000)
	pi, _ := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodVADummy, nil)

	_, _ = svc.Process(context.Background(), pi.ID, true) // first process: SUCCESS

	_, err := svc.Process(context.Background(), pi.ID, false) // second: must fail
	if !errors.Is(err, apperror.ErrInvalidState) {
		t.Fatalf("want ErrInvalidState for already-processed intent, got %v", err)
	}
}

// ── GetIntent ─────────────────────────────────────────────────────────────────

func TestPaymentService_GetIntent_HappyPath(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	svc := newPaymentSvc(invRepo, piRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	inv := seedPendingInvoice(invRepo, uuid.New(), 500)
	created, _ := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodWallet, nil)

	pi, err := svc.GetIntent(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pi.ID != created.ID {
		t.Fatalf("wrong intent returned")
	}
}

func TestPaymentService_GetIntent_NotFound(t *testing.T) {
	svc := newPaymentSvc(newMockInvoiceRepo(), newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	_, err := svc.GetIntent(context.Background(), uuid.New())
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
