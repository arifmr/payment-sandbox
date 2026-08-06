package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

// SRS §2.2 / §3.4: a top-up settles exactly once, and only SUCCESS credits the wallet.

func newTopupEnv(balance int64) (*mockTopupRepo, *mockWalletRepo, WalletService, uuid.UUID) {
	merchantID := uuid.New()
	tRepo := newMockTopupRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: balance}}
	return tRepo, wRepo, newWalletSvc(tRepo, wRepo), merchantID
}

func TestWalletService_ProcessTopup_IsNotReplayable(t *testing.T) {
	for _, first := range []bool{true, false} {
		name := "after SUCCESS"
		if !first {
			name = "after FAILED"
		}
		t.Run(name, func(t *testing.T) {
			tRepo, wRepo, svc, merchantID := newTopupEnv(1000)
			ctx := context.Background()

			topup, err := svc.RequestTopup(ctx, merchantID, 500)
			if err != nil {
				t.Fatalf("RequestTopup: %v", err)
			}
			if _, err := svc.ProcessTopup(ctx, topup.ID, first); err != nil {
				t.Fatalf("first ProcessTopup: %v", err)
			}
			balanceAfterFirst := wRepo.balances[merchantID]

			for _, second := range []bool{true, false} {
				if _, err := svc.ProcessTopup(ctx, topup.ID, second); !errors.Is(err, apperror.ErrInvalidState) {
					t.Errorf("ProcessTopup(success=%v) replayed: want ErrInvalidState, got %v", second, err)
				}
			}
			if wRepo.balances[merchantID] != balanceAfterFirst {
				t.Errorf("balance changed on replay: %d != %d", wRepo.balances[merchantID], balanceAfterFirst)
			}
			if tRepo.store[topup.ID].Status == constant.TopupPending {
				t.Error("the top-up should have left PENDING")
			}
		})
	}
}

func TestWalletService_ProcessTopup_SuccessCreditsExactAmount(t *testing.T) {
	_, wRepo, svc, merchantID := newTopupEnv(1000)
	ctx := context.Background()

	topup, err := svc.RequestTopup(ctx, merchantID, 2500)
	if err != nil {
		t.Fatalf("RequestTopup: %v", err)
	}
	settled, err := svc.ProcessTopup(ctx, topup.ID, true)
	if err != nil {
		t.Fatalf("ProcessTopup: %v", err)
	}

	if settled.Status != constant.TopupSuccess {
		t.Errorf("status = %s, want SUCCESS", settled.Status)
	}
	if settled.ProcessedAt == nil {
		t.Error("processed_at must be set once the top-up settles")
	}
	if got := wRepo.balances[merchantID]; got != 3500 {
		t.Errorf("balance = %d, want 1000 + 2500", got)
	}
}

func TestWalletService_ProcessTopup_FailedDoesNotCredit(t *testing.T) {
	_, wRepo, svc, merchantID := newTopupEnv(1000)
	ctx := context.Background()

	topup, err := svc.RequestTopup(ctx, merchantID, 2500)
	if err != nil {
		t.Fatalf("RequestTopup: %v", err)
	}
	settled, err := svc.ProcessTopup(ctx, topup.ID, false)
	if err != nil {
		t.Fatalf("ProcessTopup: %v", err)
	}

	if settled.Status != constant.TopupFailed {
		t.Errorf("status = %s, want FAILED", settled.Status)
	}
	if got := wRepo.balances[merchantID]; got != 1000 {
		t.Errorf("balance = %d, want it unchanged at 1000", got)
	}
}

// SRS §3.4: top-up SUCCESS is atomic. A failing credit must leave the top-up PENDING
// so an admin can retry it.
func TestWalletService_ProcessTopup_RollsBackWhenWalletMissing(t *testing.T) {
	merchantID := uuid.New()
	tRepo := newMockTopupRepo()
	// No wallet row → AddBalance fails with ErrNotFound.
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{}}
	svc := NewWalletService(tRepo, wRepo, topupSnapshotUoW{topups: tRepo})
	ctx := context.Background()

	topup, err := svc.RequestTopup(ctx, merchantID, 500)
	if err != nil {
		t.Fatalf("RequestTopup: %v", err)
	}

	if _, err := svc.ProcessTopup(ctx, topup.ID, true); err == nil {
		t.Fatal("expected ProcessTopup to fail without a wallet")
	}
	if got := tRepo.store[topup.ID].Status; got != constant.TopupPending {
		t.Errorf("status = %s, want it rolled back to PENDING", got)
	}

	// After the rollback a retry must still be possible.
	wRepo.balances[merchantID] = 0
	if _, err := svc.ProcessTopup(ctx, topup.ID, true); err != nil {
		t.Fatalf("retry after rollback failed: %v", err)
	}
	if got := wRepo.balances[merchantID]; got != 500 {
		t.Errorf("balance = %d, want 500 after the retry", got)
	}
}

// SRS §4.5: amount must be positive.
func TestWalletService_RequestTopup_RejectsNonPositiveAmount(t *testing.T) {
	tRepo, _, svc, merchantID := newTopupEnv(0)

	for _, amount := range []int64{0, -1, -9999} {
		if _, err := svc.RequestTopup(context.Background(), merchantID, amount); !errors.Is(err, apperror.ErrInvalidAmount) {
			t.Errorf("RequestTopup(%d): want ErrInvalidAmount, got %v", amount, err)
		}
	}
	if len(tRepo.store) != 0 {
		t.Errorf("an invalid top-up was persisted: %d rows", len(tRepo.store))
	}
}

// A top-up must never credit the wallet on its own (SRS §2.2).
func TestWalletService_RequestTopup_DoesNotCreditImmediately(t *testing.T) {
	_, wRepo, svc, merchantID := newTopupEnv(1000)

	topup, err := svc.RequestTopup(context.Background(), merchantID, 5000)
	if err != nil {
		t.Fatalf("RequestTopup: %v", err)
	}
	if topup.Status != constant.TopupPending {
		t.Errorf("status = %s, want PENDING", topup.Status)
	}
	if topup.ProcessedAt != nil {
		t.Error("processed_at must be nil while the top-up is pending")
	}
	if got := wRepo.balances[merchantID]; got != 1000 {
		t.Errorf("balance = %d — requesting a top-up must not credit the wallet", got)
	}
}

func TestWalletService_ListTopups_ScopedByMerchant(t *testing.T) {
	tRepo, _, svc, merchantID := newTopupEnv(0)
	other := uuid.New()
	ctx := context.Background()

	if _, err := svc.RequestTopup(ctx, merchantID, 100); err != nil {
		t.Fatalf("RequestTopup: %v", err)
	}
	if _, err := svc.RequestTopup(ctx, merchantID, 200); err != nil {
		t.Fatalf("RequestTopup: %v", err)
	}
	if _, err := svc.RequestTopup(ctx, other, 300); err != nil {
		t.Fatalf("RequestTopup: %v", err)
	}

	mine, total, err := svc.ListTopups(ctx, &merchantID, 0, 10)
	if err != nil {
		t.Fatalf("ListTopups: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	for _, tp := range mine {
		if tp.MerchantID != merchantID {
			t.Errorf("got a top-up belonging to %s", tp.MerchantID)
		}
	}

	// A nil filter is the admin view: everything.
	_, all, err := svc.ListTopups(ctx, nil, 0, 10)
	if err != nil {
		t.Fatalf("ListTopups(nil): %v", err)
	}
	if all != 3 {
		t.Errorf("admin total = %d, want 3", all)
	}
	if len(tRepo.store) != 3 {
		t.Errorf("store has %d rows, want 3", len(tRepo.store))
	}
}

// topupSnapshotUoW restores top-up state when fn fails, mimicking a real rollback.
type topupSnapshotUoW struct{ topups *mockTopupRepo }

func (u topupSnapshotUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	snap := map[uuid.UUID]model.Topup{}
	for id, t := range u.topups.store {
		snap[id] = *t
	}
	if err := fn(ctx); err != nil {
		for id, t := range snap {
			restored := t
			u.topups.store[id] = &restored
		}
		return err
	}
	return nil
}
