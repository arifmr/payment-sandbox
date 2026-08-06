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

// ── mockTopupRepo ─────────────────────────────────────────────────────────────

type mockTopupRepo struct {
	store map[uuid.UUID]*model.Topup
}

func newMockTopupRepo() *mockTopupRepo {
	return &mockTopupRepo{store: map[uuid.UUID]*model.Topup{}}
}

func (m *mockTopupRepo) Create(_ context.Context, t *model.Topup) error {
	t.CreatedAt = time.Now()
	m.store[t.ID] = t
	return nil
}

func (m *mockTopupRepo) FindByID(_ context.Context, id uuid.UUID) (*model.Topup, error) {
	t, ok := m.store[id]
	if !ok {
		return nil, apperror.ErrNotFound
	}
	return t, nil
}

func (m *mockTopupRepo) UpdateStatus(_ context.Context, id uuid.UUID, status constant.TopupStatus, processedAt *time.Time) error {
	t, ok := m.store[id]
	if !ok {
		return apperror.ErrNotFound
	}
	// Mirror the real repo: the UPDATE carries `AND status = 'PENDING'`, so a
	// non-pending topup affects zero rows.
	if t.Status != constant.TopupPending {
		return apperror.ErrInvalidState
	}
	t.Status = status
	if processedAt != nil {
		t.ProcessedAt = processedAt
	}
	return nil
}

func (m *mockTopupRepo) List(_ context.Context, merchantID *uuid.UUID, offset, limit int) ([]model.Topup, int64, error) {
	var result []model.Topup
	for _, t := range m.store {
		if merchantID == nil || t.MerchantID == *merchantID {
			result = append(result, *t)
		}
	}
	return result, int64(len(result)), nil
}

var _ repository.TopupRepository = (*mockTopupRepo)(nil)

// ── helpers ───────────────────────────────────────────────────────────────────

func newWalletSvc(tRepo *mockTopupRepo, wRepo *mockWalletRepo) WalletService {
	return NewWalletService(tRepo, wRepo, noopUoW{})
}

// ── GetBalance ────────────────────────────────────────────────────────────────

func TestWalletService_GetBalance_HappyPath(t *testing.T) {
	merchantID := uuid.New()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}}
	svc := newWalletSvc(newMockTopupRepo(), wRepo)

	wallet, err := svc.GetBalance(context.Background(), merchantID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wallet.Balance != 5000 {
		t.Fatalf("want balance 5000, got %d", wallet.Balance)
	}
}

func TestWalletService_GetBalance_NotFound(t *testing.T) {
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{}}
	svc := newWalletSvc(newMockTopupRepo(), wRepo)

	_, err := svc.GetBalance(context.Background(), uuid.New())
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ── RequestTopup ──────────────────────────────────────────────────────────────

func TestWalletService_RequestTopup_HappyPath(t *testing.T) {
	merchantID := uuid.New()
	tRepo := newMockTopupRepo()
	svc := newWalletSvc(tRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	topup, err := svc.RequestTopup(context.Background(), merchantID, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if topup.Amount != 1000 {
		t.Fatalf("want amount 1000, got %d", topup.Amount)
	}
	if topup.Status != constant.TopupPending {
		t.Fatalf("want status PENDING, got %s", topup.Status)
	}
	if topup.MerchantID != merchantID {
		t.Fatalf("want merchantID %s, got %s", merchantID, topup.MerchantID)
	}
	if _, ok := tRepo.store[topup.ID]; !ok {
		t.Fatal("topup not persisted in repository")
	}
}

func TestWalletService_RequestTopup_InvalidAmount(t *testing.T) {
	svc := newWalletSvc(newMockTopupRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	for _, amount := range []int64{0, -1, -999} {
		_, err := svc.RequestTopup(context.Background(), uuid.New(), amount)
		if !errors.Is(err, apperror.ErrInvalidAmount) {
			t.Errorf("RequestTopup(%d): want ErrInvalidAmount, got %v", amount, err)
		}
	}
}

// ── ProcessTopup ──────────────────────────────────────────────────────────────

func TestWalletService_ProcessTopup_Success(t *testing.T) {
	merchantID := uuid.New()
	tRepo := newMockTopupRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newWalletSvc(tRepo, wRepo)

	topup, _ := svc.RequestTopup(context.Background(), merchantID, 2000)

	result, err := svc.ProcessTopup(context.Background(), topup.ID, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != constant.TopupSuccess {
		t.Fatalf("want TopupSuccess, got %s", result.Status)
	}
	if result.ProcessedAt == nil {
		t.Fatal("ProcessedAt must be set on success")
	}
	if wRepo.balances[merchantID] != 2000 {
		t.Fatalf("want balance 2000, got %d", wRepo.balances[merchantID])
	}
}

func TestWalletService_ProcessTopup_Failed(t *testing.T) {
	merchantID := uuid.New()
	tRepo := newMockTopupRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newWalletSvc(tRepo, wRepo)

	topup, _ := svc.RequestTopup(context.Background(), merchantID, 2000)

	result, err := svc.ProcessTopup(context.Background(), topup.ID, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != constant.TopupFailed {
		t.Fatalf("want TopupFailed, got %s", result.Status)
	}
	if wRepo.balances[merchantID] != 0 {
		t.Fatalf("wallet must not be credited on failure, got balance %d", wRepo.balances[merchantID])
	}
}

func TestWalletService_ProcessTopup_NotFound(t *testing.T) {
	svc := newWalletSvc(newMockTopupRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	_, err := svc.ProcessTopup(context.Background(), uuid.New(), true)
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestWalletService_ProcessTopup_InvalidState(t *testing.T) {
	merchantID := uuid.New()
	tRepo := newMockTopupRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newWalletSvc(tRepo, wRepo)

	topup, _ := svc.RequestTopup(context.Background(), merchantID, 1000)
	_, _ = svc.ProcessTopup(context.Background(), topup.ID, true) // first process: SUCCESS

	_, err := svc.ProcessTopup(context.Background(), topup.ID, false) // second: must fail
	if !errors.Is(err, apperror.ErrInvalidState) {
		t.Fatalf("want ErrInvalidState for already-processed topup, got %v", err)
	}
}

// ── ListTopups ────────────────────────────────────────────────────────────────

func TestWalletService_ListTopups_FilteredByMerchant(t *testing.T) {
	merchantID := uuid.New()
	otherID := uuid.New()
	tRepo := newMockTopupRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0, otherID: 0}}
	svc := newWalletSvc(tRepo, wRepo)

	svc.RequestTopup(context.Background(), merchantID, 1000)
	svc.RequestTopup(context.Background(), merchantID, 2000)
	svc.RequestTopup(context.Background(), otherID, 500)

	topups, total, err := svc.ListTopups(context.Background(), &merchantID, 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Fatalf("want 2 topups for merchant, got %d", total)
	}
	for _, tp := range topups {
		if tp.MerchantID != merchantID {
			t.Fatalf("got topup for wrong merchant %s", tp.MerchantID)
		}
	}
}

func TestWalletService_ListTopups_AllMerchants(t *testing.T) {
	merchantID := uuid.New()
	otherID := uuid.New()
	tRepo := newMockTopupRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0, otherID: 0}}
	svc := newWalletSvc(tRepo, wRepo)

	svc.RequestTopup(context.Background(), merchantID, 1000)
	svc.RequestTopup(context.Background(), otherID, 500)

	_, total, err := svc.ListTopups(context.Background(), nil, 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Fatalf("want 2 total topups, got %d", total)
	}
}
