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

// noopUoW runs the function without a real DB transaction. Suitable for unit tests
// that only exercise business logic against in-memory mocks.
type noopUoW struct{}

func (noopUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// in-memory mocks

type mockRefundRepo struct{ store map[uuid.UUID]*model.Refund }

func newMockRefundRepo() *mockRefundRepo { return &mockRefundRepo{store: map[uuid.UUID]*model.Refund{}} }

func (m *mockRefundRepo) Create(_ context.Context, r *model.Refund) error {
	r.CreatedAt = time.Now()
	m.store[r.ID] = r
	return nil
}
func (m *mockRefundRepo) FindByID(_ context.Context, id uuid.UUID) (*model.Refund, error) {
	r, ok := m.store[id]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return r, nil
}
func (m *mockRefundRepo) UpdateStatus(_ context.Context, id uuid.UUID, from, to constant.RefundStatus, processedAt *time.Time) error {
	r, ok := m.store[id]
	if !ok {
		return apperror.ErrNotFound
	}
	if r.Status != from {
		return apperror.ErrInvalidState
	}
	r.Status = to
	if processedAt != nil {
		r.ProcessedAt = processedAt
	}
	return nil
}
func (m *mockRefundRepo) ListByMerchant(context.Context, uuid.UUID, int, int) ([]model.Refund, int64, error) {
	return nil, 0, nil
}
func (m *mockRefundRepo) List(context.Context, int, int) ([]model.Refund, int64, error) {
	return nil, 0, nil
}

type mockWalletRepo struct {
	balances map[uuid.UUID]int64
	debited  bool
}

func (m *mockWalletRepo) Create(context.Context, *model.Wallet) error { return nil }
func (m *mockWalletRepo) FindByMerchantID(_ context.Context, id uuid.UUID) (*model.Wallet, error) {
	if b, ok := m.balances[id]; ok {
		return &model.Wallet{MerchantID: id, Balance: b}, nil
	}
	return nil, apperror.ErrNotFound
}
func (m *mockWalletRepo) AddBalance(_ context.Context, id uuid.UUID, delta int64) error {
	cur, ok := m.balances[id]
	if !ok {
		return apperror.ErrNotFound
	}
	if cur+delta < 0 {
		return apperror.ErrInsufficientFunds
	}
	m.balances[id] = cur + delta
	if delta < 0 {
		m.debited = true
	}
	return nil
}

func TestRefundService_AdminAction_HappyPath(t *testing.T) {
	merchantID := uuid.New()
	rid := uuid.New()
	rfRepo := newMockRefundRepo()
	rfRepo.store[rid] = &model.Refund{
		ID:         rid,
		MerchantID: merchantID,
		Amount:     1000,
		Status:     constant.RefundRequested,
	}
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}}

	svc := NewRefundService(rfRepo, nil, nil, wRepo, noopUoW{})

	// REQUESTED -> APPROVED
	rf, err := svc.AdminAction(context.Background(), rid, RefundActionApprove)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if rf.Status != constant.RefundApproved {
		t.Fatalf("want APPROVED, got %s", rf.Status)
	}

	// APPROVED -> SUCCESS (debits wallet)
	rf, err = svc.AdminAction(context.Background(), rid, RefundActionProcess)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if rf.Status != constant.RefundSuccess {
		t.Fatalf("want SUCCESS, got %s", rf.Status)
	}
	if !wRepo.debited {
		t.Fatalf("wallet not debited")
	}
	if wRepo.balances[merchantID] != 4000 {
		t.Fatalf("balance want 4000, got %d", wRepo.balances[merchantID])
	}
}

func TestRefundService_AdminAction_InvalidTransition(t *testing.T) {
	merchantID := uuid.New()
	rid := uuid.New()
	rfRepo := newMockRefundRepo()
	rfRepo.store[rid] = &model.Refund{
		ID:         rid,
		MerchantID: merchantID,
		Amount:     1000,
		Status:     constant.RefundRequested,
	}
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}}
	svc := NewRefundService(rfRepo, nil, nil, wRepo, noopUoW{})

	// PROCESS without prior APPROVE should fail.
	if _, err := svc.AdminAction(context.Background(), rid, RefundActionProcess); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

// guard: ensure repository.RefundRepository interface stays compatible with the mock.
var _ repository.RefundRepository = (*mockRefundRepo)(nil)
var _ repository.WalletRepository = (*mockWalletRepo)(nil)

// ── Request ───────────────────────────────────────────────────────────────────

func newRefundSvc(rf *mockRefundRepo, inv *mockInvoiceRepo, pi *mockPaymentIntentRepo, w *mockWalletRepo) RefundService {
	return NewRefundService(rf, inv, pi, w, noopUoW{})
}

// seedPaidInvoiceWithIntent creates a PAID invoice and a corresponding SUCCESS
// payment intent so that refund.Request can find the latest success.
func seedPaidInvoiceWithIntent(invRepo *mockInvoiceRepo, piRepo *mockPaymentIntentRepo, merchantID uuid.UUID, amount int64) (*model.Invoice, *model.PaymentIntent) {
	inv := &model.Invoice{
		ID:         uuid.New(),
		MerchantID: merchantID,
		Amount:     amount,
		Status:     constant.InvoicePaid,
		DueDate:    time.Now().Add(24 * time.Hour),
	}
	invRepo.store[inv.ID] = inv

	pi := &model.PaymentIntent{
		ID:        uuid.New(),
		InvoiceID: inv.ID,
		Method:    constant.PaymentMethodWallet,
		Status:    constant.PaymentSuccess,
		Amount:    amount,
	}
	piRepo.store[pi.ID] = pi
	piRepo.latestSuccess[inv.ID] = pi

	return inv, pi
}

func TestRefundService_Request_HappyPath(t *testing.T) {
	merchantID := uuid.New()
	rfRepo := newMockRefundRepo()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}}

	inv, _ := seedPaidInvoiceWithIntent(invRepo, piRepo, merchantID, 2000)
	svc := newRefundSvc(rfRepo, invRepo, piRepo, wRepo)

	rf, err := svc.Request(context.Background(), merchantID, inv.ID, 500, "defective product")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rf.Status != constant.RefundRequested {
		t.Fatalf("want status REQUESTED, got %s", rf.Status)
	}
	if rf.Amount != 500 {
		t.Fatalf("want amount 500, got %d", rf.Amount)
	}
	if rf.MerchantID != merchantID {
		t.Fatalf("wrong merchantID on refund")
	}
	if _, ok := rfRepo.store[rf.ID]; !ok {
		t.Fatal("refund not persisted in repository")
	}
}

func TestRefundService_Request_InvalidAmount(t *testing.T) {
	svc := newRefundSvc(newMockRefundRepo(), newMockInvoiceRepo(), newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	for _, amount := range []int64{0, -1} {
		_, err := svc.Request(context.Background(), uuid.New(), uuid.New(), amount, "reason")
		if !errors.Is(err, apperror.ErrInvalidAmount) {
			t.Errorf("Request(amount=%d): want ErrInvalidAmount, got %v", amount, err)
		}
	}
}

func TestRefundService_Request_InvoiceNotFound(t *testing.T) {
	svc := newRefundSvc(newMockRefundRepo(), newMockInvoiceRepo(), newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	_, err := svc.Request(context.Background(), uuid.New(), uuid.New(), 100, "reason")
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRefundService_Request_Forbidden(t *testing.T) {
	merchantID := uuid.New()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()

	inv, _ := seedPaidInvoiceWithIntent(invRepo, piRepo, merchantID, 2000)
	svc := newRefundSvc(newMockRefundRepo(), invRepo, piRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	_, err := svc.Request(context.Background(), uuid.New(), inv.ID, 100, "reason") // wrong merchant
	if !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestRefundService_Request_InvoiceNotPaid(t *testing.T) {
	merchantID := uuid.New()
	invRepo := newMockInvoiceRepo()

	inv := &model.Invoice{
		ID:         uuid.New(),
		MerchantID: merchantID,
		Amount:     2000,
		Status:     constant.InvoicePending, // not paid
	}
	invRepo.store[inv.ID] = inv
	svc := newRefundSvc(newMockRefundRepo(), invRepo, newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	_, err := svc.Request(context.Background(), merchantID, inv.ID, 100, "reason")
	if err == nil {
		t.Fatal("want error for non-PAID invoice, got nil")
	}
}

func TestRefundService_Request_ExceedsInvoiceAmount(t *testing.T) {
	merchantID := uuid.New()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()

	inv, _ := seedPaidInvoiceWithIntent(invRepo, piRepo, merchantID, 1000)
	svc := newRefundSvc(newMockRefundRepo(), invRepo, piRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	_, err := svc.Request(context.Background(), merchantID, inv.ID, 9999, "reason") // exceeds 1000
	if err == nil {
		t.Fatal("want error when refund amount exceeds invoice amount, got nil")
	}
}

// ── AdminAction — additional transitions ──────────────────────────────────────

func TestRefundService_AdminAction_Reject(t *testing.T) {
	merchantID := uuid.New()
	rid := uuid.New()
	rfRepo := newMockRefundRepo()
	rfRepo.store[rid] = &model.Refund{
		ID:         rid,
		MerchantID: merchantID,
		Amount:     500,
		Status:     constant.RefundRequested,
	}
	svc := NewRefundService(rfRepo, nil, nil, &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}}, noopUoW{})

	rf, err := svc.AdminAction(context.Background(), rid, RefundActionReject)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rf.Status != constant.RefundRejected {
		t.Fatalf("want REJECTED, got %s", rf.Status)
	}
	if rf.ProcessedAt == nil {
		t.Fatal("ProcessedAt must be set on reject")
	}
}

func TestRefundService_AdminAction_Fail(t *testing.T) {
	merchantID := uuid.New()
	rid := uuid.New()
	rfRepo := newMockRefundRepo()
	rfRepo.store[rid] = &model.Refund{
		ID:         rid,
		MerchantID: merchantID,
		Amount:     500,
		Status:     constant.RefundApproved, // already approved
	}
	svc := NewRefundService(rfRepo, nil, nil, &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}}, noopUoW{})

	rf, err := svc.AdminAction(context.Background(), rid, RefundActionFail)
	if err != nil {
		t.Fatalf("fail action: %v", err)
	}
	if rf.Status != constant.RefundFailed {
		t.Fatalf("want FAILED, got %s", rf.Status)
	}
}

func TestRefundService_AdminAction_InvalidAction(t *testing.T) {
	rfRepo := newMockRefundRepo()
	rid := uuid.New()
	rfRepo.store[rid] = &model.Refund{ID: rid, Status: constant.RefundRequested}
	svc := NewRefundService(rfRepo, nil, nil, &mockWalletRepo{balances: map[uuid.UUID]int64{}}, noopUoW{})

	_, err := svc.AdminAction(context.Background(), rid, RefundAction("UNKNOWN"))
	if err == nil {
		t.Fatal("want error for invalid action, got nil")
	}
}

// ── GetByID / List / ListByMerchant ───────────────────────────────────────────

func TestRefundService_GetByID_HappyPath(t *testing.T) {
	rfRepo := newMockRefundRepo()
	rid := uuid.New()
	rfRepo.store[rid] = &model.Refund{ID: rid, Status: constant.RefundRequested}
	svc := NewRefundService(rfRepo, nil, nil, nil, noopUoW{})

	rf, err := svc.GetByID(context.Background(), rid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rf.ID != rid {
		t.Fatalf("wrong refund returned")
	}
}

func TestRefundService_GetByID_NotFound(t *testing.T) {
	svc := NewRefundService(newMockRefundRepo(), nil, nil, nil, noopUoW{})

	_, err := svc.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRefundService_ListByMerchant(t *testing.T) {
	rfRepo := newMockRefundRepo()
	svc := NewRefundService(rfRepo, nil, nil, nil, noopUoW{})

	refunds, total, err := svc.ListByMerchant(context.Background(), uuid.New(), 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0 {
		t.Fatalf("want 0 refunds, got %d", total)
	}
	_ = refunds
}

func TestRefundService_List(t *testing.T) {
	rfRepo := newMockRefundRepo()
	svc := NewRefundService(rfRepo, nil, nil, nil, noopUoW{})

	refunds, total, err := svc.List(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0 {
		t.Fatalf("want 0 refunds, got %d", total)
	}
	_ = refunds
}
