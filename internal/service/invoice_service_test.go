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

// ── mockInvoiceRepo ───────────────────────────────────────────────────────────

type mockInvoiceRepo struct {
	store      map[uuid.UUID]*model.Invoice
	tokenIndex map[string]*model.Invoice

	// createErrs is consumed one entry per Create call, letting a test simulate
	// unique-constraint collisions before a success.
	createErrs  []error
	createCalls int
	// lockedForUpdate counts FindByIDForUpdate calls.
	lockedForUpdate int
}

func newMockInvoiceRepo() *mockInvoiceRepo {
	return &mockInvoiceRepo{
		store:      map[uuid.UUID]*model.Invoice{},
		tokenIndex: map[string]*model.Invoice{},
	}
}

func (m *mockInvoiceRepo) Create(_ context.Context, inv *model.Invoice) error {
	m.createCalls++
	if len(m.createErrs) > 0 {
		err := m.createErrs[0]
		m.createErrs = m.createErrs[1:]
		if err != nil {
			return err
		}
	}
	if _, clash := m.tokenIndex[inv.PaymentToken]; clash {
		return apperror.New(apperror.KindConflict, "DUPLICATE", "payment_token already exists")
	}
	inv.CreatedAt = time.Now()
	m.store[inv.ID] = inv
	m.tokenIndex[inv.PaymentToken] = inv
	return nil
}

func (m *mockInvoiceRepo) FindByID(_ context.Context, id uuid.UUID) (*model.Invoice, error) {
	inv, ok := m.store[id]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return inv, nil
}

// FindByIDForUpdate has no separate locking semantics in-memory; the tests that
// care about serialization assert on call ordering instead.
func (m *mockInvoiceRepo) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*model.Invoice, error) {
	m.lockedForUpdate++
	return m.FindByID(ctx, id)
}

func (m *mockInvoiceRepo) FindByPaymentToken(_ context.Context, token string) (*model.Invoice, error) {
	inv, ok := m.tokenIndex[token]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return inv, nil
}

func (m *mockInvoiceRepo) UpdateStatus(_ context.Context, id uuid.UUID, from, to constant.InvoiceStatus, paidAt *time.Time) error {
	inv, ok := m.store[id]
	if !ok {
		return apperror.ErrNotFound
	}
	if inv.Status != from {
		return apperror.ErrInvalidState
	}
	inv.Status = to
	if paidAt != nil {
		inv.PaidAt = paidAt
	}
	return nil
}

func (m *mockInvoiceRepo) List(_ context.Context, f repository.InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error) {
	var result []model.Invoice
	for _, inv := range m.store {
		if f.MerchantID == nil || inv.MerchantID == *f.MerchantID {
			result = append(result, *inv)
		}
	}
	return result, int64(len(result)), nil
}

func (m *mockInvoiceRepo) MarkExpired(_ context.Context, now time.Time) (int64, error) {
	var count int64
	for _, inv := range m.store {
		if inv.Status == constant.InvoicePending && inv.DueDate.Before(now) {
			inv.Status = constant.InvoiceExpired
			count++
		}
	}
	return count, nil
}

var _ repository.InvoiceRepository = (*mockInvoiceRepo)(nil)

// ── helpers ───────────────────────────────────────────────────────────────────

// newInvoiceSvc builds the service with a lock that always grants, which is the
// single-instance case. Tests that care about contention pass their own locker.
func newInvoiceSvc(repo *mockInvoiceRepo) InvoiceService {
	return NewInvoiceService(repo, newMockPaymentIntentRepo(), grantingLocker{}, noopUoW{})
}

// grantingLocker always acquires: models a single running instance.
type grantingLocker struct{}

func (grantingLocker) TryLockTx(context.Context, repository.AdvisoryLockKey) (bool, error) {
	return true, nil
}

// denyingLocker never acquires: models another replica already sweeping.
type denyingLocker struct{}

func (denyingLocker) TryLockTx(context.Context, repository.AdvisoryLockKey) (bool, error) {
	return false, nil
}

var (
	_ repository.AdvisoryLocker = grantingLocker{}
	_ repository.AdvisoryLocker = denyingLocker{}
)

func validCreateInput(merchantID uuid.UUID) CreateInvoiceInput {
	return CreateInvoiceInput{
		MerchantID:    merchantID,
		CustomerName:  "Alice",
		CustomerEmail: "alice@example.com",
		Description:   "Test invoice",
		Amount:        5000,
		DueDate:       time.Now().Add(24 * time.Hour),
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestInvoiceService_Create_HappyPath(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)
	merchantID := uuid.New()

	inv, err := svc.Create(context.Background(), validCreateInput(merchantID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.Status != constant.InvoicePending {
		t.Fatalf("want status PENDING, got %s", inv.Status)
	}
	if inv.Amount != 5000 {
		t.Fatalf("want amount 5000, got %d", inv.Amount)
	}
	if inv.PaymentToken == "" {
		t.Fatal("PaymentToken must not be empty")
	}
	if inv.InvoiceNumber == "" {
		t.Fatal("InvoiceNumber must not be empty")
	}
	if inv.MerchantID != merchantID {
		t.Fatalf("wrong MerchantID: got %s", inv.MerchantID)
	}
	if _, ok := repo.store[inv.ID]; !ok {
		t.Fatal("invoice not persisted in repository")
	}
}

func TestInvoiceService_Create_InvalidAmount(t *testing.T) {
	svc := newInvoiceSvc(newMockInvoiceRepo())

	for _, amount := range []int64{0, -1, -999} {
		in := validCreateInput(uuid.New())
		in.Amount = amount
		_, err := svc.Create(context.Background(), in)
		if !errors.Is(err, apperror.ErrInvalidAmount) {
			t.Errorf("Create(amount=%d): want ErrInvalidAmount, got %v", amount, err)
		}
	}
}

func TestInvoiceService_Create_PastDueDate(t *testing.T) {
	svc := newInvoiceSvc(newMockInvoiceRepo())
	in := validCreateInput(uuid.New())
	in.DueDate = time.Now().Add(-time.Hour) // in the past

	_, err := svc.Create(context.Background(), in)
	if err == nil {
		t.Fatal("want error for past due_date, got nil")
	}
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestInvoiceService_GetByID_HappyPath(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)
	merchantID := uuid.New()

	created, _ := svc.Create(context.Background(), validCreateInput(merchantID))

	inv, err := svc.GetByID(context.Background(), created.ID, merchantID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.ID != created.ID {
		t.Fatalf("wrong invoice returned: got %s", inv.ID)
	}
}

func TestInvoiceService_GetByID_NotFound(t *testing.T) {
	svc := newInvoiceSvc(newMockInvoiceRepo())

	_, err := svc.GetByID(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestInvoiceService_GetByID_Forbidden(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)
	merchantID := uuid.New()

	created, _ := svc.Create(context.Background(), validCreateInput(merchantID))

	_, err := svc.GetByID(context.Background(), created.ID, uuid.New()) // different merchant
	if !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

// ── GetByPaymentToken ─────────────────────────────────────────────────────────

func TestInvoiceService_GetByPaymentToken_HappyPath(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)

	created, _ := svc.Create(context.Background(), validCreateInput(uuid.New()))

	inv, err := svc.GetByPaymentToken(context.Background(), created.PaymentToken)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inv.ID != created.ID {
		t.Fatalf("wrong invoice returned")
	}
}

func TestInvoiceService_GetByPaymentToken_NotFound(t *testing.T) {
	svc := newInvoiceSvc(newMockInvoiceRepo())

	_, err := svc.GetByPaymentToken(context.Background(), "nonexistent-token")
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestInvoiceService_List(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)
	merchantID := uuid.New()
	otherID := uuid.New()

	svc.Create(context.Background(), validCreateInput(merchantID))
	svc.Create(context.Background(), validCreateInput(merchantID))
	svc.Create(context.Background(), validCreateInput(otherID))

	invoices, total, err := svc.List(context.Background(), repository.InvoiceFilter{MerchantID: &merchantID}, 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Fatalf("want 2 invoices for merchant, got %d", total)
	}
	for _, inv := range invoices {
		if inv.MerchantID != merchantID {
			t.Fatalf("got invoice for wrong merchant")
		}
	}
}

// ── ExpireDue ─────────────────────────────────────────────────────────────────

func TestInvoiceService_ExpireDue(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)
	merchantID := uuid.New()

	// This invoice is still in the future — must NOT be expired.
	futureInv, _ := svc.Create(context.Background(), validCreateInput(merchantID))

	// Directly insert an overdue invoice into the mock.
	overdueID := uuid.New()
	repo.store[overdueID] = &model.Invoice{
		ID:         overdueID,
		MerchantID: merchantID,
		Amount:     1000,
		Status:     constant.InvoicePending,
		DueDate:    time.Now().Add(-time.Hour), // past due
	}

	res, err := svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.InvoicesExpired != 1 {
		t.Fatalf("want 1 expired invoice, got %d", res.InvoicesExpired)
	}
	if repo.store[overdueID].Status != constant.InvoiceExpired {
		t.Fatalf("overdue invoice must be EXPIRED, got %s", repo.store[overdueID].Status)
	}
	if repo.store[futureInv.ID].Status != constant.InvoicePending {
		t.Fatalf("future invoice must remain PENDING, got %s", repo.store[futureInv.ID].Status)
	}
}
