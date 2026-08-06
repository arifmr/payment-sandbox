package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/repository"
)

// ── GetIntentByToken (SRS §4.3) ───────────────────────────────────────────────

func TestPaymentService_GetIntentByToken_HappyPath(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	svc := newPaymentSvc(invRepo, piRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	inv := seedPendingInvoice(invRepo, uuid.New(), 1000)
	intent, err := svc.CreateIntent(context.Background(), inv.PaymentToken, constant.PaymentMethodVADummy, nil)
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	got, err := svc.GetIntentByToken(context.Background(), inv.PaymentToken, intent.ID)
	if err != nil {
		t.Fatalf("GetIntentByToken: %v", err)
	}
	if got.ID != intent.ID {
		t.Errorf("returned intent %s, want %s", got.ID, intent.ID)
	}
}

// Possession of a payment token must not grant access to another invoice's intent.
func TestPaymentService_GetIntentByToken_ForeignIntentIsNotFound(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	svc := newPaymentSvc(invRepo, piRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	mine := seedPendingInvoice(invRepo, uuid.New(), 1000)
	theirs := seedPendingInvoice(invRepo, uuid.New(), 2000)

	theirIntent, err := svc.CreateIntent(context.Background(), theirs.PaymentToken, constant.PaymentMethodVADummy, nil)
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	// Asking with my token for their intent must look like it does not exist.
	if _, err := svc.GetIntentByToken(context.Background(), mine.PaymentToken, theirIntent.ID); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPaymentService_GetIntentByToken_UnknownTokenIsNotFound(t *testing.T) {
	svc := newPaymentSvc(newMockInvoiceRepo(), newMockPaymentIntentRepo(), &mockWalletRepo{balances: map[uuid.UUID]int64{}})

	if _, err := svc.GetIntentByToken(context.Background(), "no-such-token", uuid.New()); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ── ListIntents (SRS §4.4 admin search) ───────────────────────────────────────

func TestPaymentService_ListIntents_FiltersByInvoiceAndStatus(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	merchantID := uuid.New()
	svc := newPaymentSvc(invRepo, piRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}})
	ctx := context.Background()

	first := seedPendingInvoice(invRepo, merchantID, 1000)
	second := seedPendingInvoice(invRepo, merchantID, 2000)

	a, _ := svc.CreateIntent(ctx, first.PaymentToken, constant.PaymentMethodVADummy, nil)
	_, _ = svc.CreateIntent(ctx, first.PaymentToken, constant.PaymentMethodWallet, nil)
	_, _ = svc.CreateIntent(ctx, second.PaymentToken, constant.PaymentMethodVADummy, nil)

	// By invoice.
	items, total, err := svc.ListIntents(ctx, repository.PaymentIntentFilter{InvoiceID: &first.ID}, 0, 10)
	if err != nil {
		t.Fatalf("ListIntents: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("total = %d (len %d), want 2 intents for the first invoice", total, len(items))
	}

	// Mark one SUCCESS, then filter by status.
	if _, err := svc.Process(ctx, a.ID, true); err != nil {
		t.Fatalf("Process: %v", err)
	}
	success := constant.PaymentSuccess
	items, total, err = svc.ListIntents(ctx, repository.PaymentIntentFilter{Status: &success}, 0, 10)
	if err != nil {
		t.Fatalf("ListIntents: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != a.ID {
		t.Fatalf("status filter did not isolate the SUCCESS intent: %+v", items)
	}
}

func TestPaymentService_ListIntents_NoFilterReturnsEverything(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	svc := newPaymentSvc(invRepo, piRepo, &mockWalletRepo{balances: map[uuid.UUID]int64{}})
	ctx := context.Background()

	inv := seedPendingInvoice(invRepo, uuid.New(), 1000)
	for i := 0; i < 3; i++ {
		if _, err := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodVADummy, nil); err != nil {
			t.Fatalf("CreateIntent: %v", err)
		}
	}

	_, total, err := svc.ListIntents(ctx, repository.PaymentIntentFilter{}, 0, 10)
	if err != nil {
		t.Fatalf("ListIntents: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
}

// ── invariants around Process ─────────────────────────────────────────────────

// SRS §3.4: an intent may only be finalized once, whichever way it went.
func TestPaymentService_Process_IsNotReplayable(t *testing.T) {
	for _, first := range []bool{true, false} {
		t.Run(map[bool]string{true: "after SUCCESS", false: "after FAILED"}[first], func(t *testing.T) {
			invRepo := newMockInvoiceRepo()
			piRepo := newMockPaymentIntentRepo()
			merchantID := uuid.New()
			wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
			svc := newPaymentSvc(invRepo, piRepo, wRepo)
			ctx := context.Background()

			inv := seedPendingInvoice(invRepo, merchantID, 3000)
			intent, err := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodVADummy, nil)
			if err != nil {
				t.Fatalf("CreateIntent: %v", err)
			}

			if _, err := svc.Process(ctx, intent.ID, first); err != nil {
				t.Fatalf("first Process: %v", err)
			}
			balanceAfterFirst := wRepo.balances[merchantID]

			// Replaying in either direction must be refused.
			for _, second := range []bool{true, false} {
				if _, err := svc.Process(ctx, intent.ID, second); err == nil {
					t.Fatalf("Process(success=%v) was replayable", second)
				}
			}
			if wRepo.balances[merchantID] != balanceAfterFirst {
				t.Errorf("balance changed on a replayed Process: %d != %d", wRepo.balances[merchantID], balanceAfterFirst)
			}
		})
	}
}

// SRS §3.4: a FAILED payment must leave the invoice payable.
func TestPaymentService_Process_FailedLeavesInvoicePending(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	merchantID := uuid.New()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)
	ctx := context.Background()

	inv := seedPendingInvoice(invRepo, merchantID, 3000)
	intent, err := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodVADummy, nil)
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if _, err := svc.Process(ctx, intent.ID, false); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if invRepo.store[inv.ID].Status != constant.InvoicePending {
		t.Errorf("invoice status = %s, want it still PENDING after a failed payment", invRepo.store[inv.ID].Status)
	}
	if wRepo.balances[merchantID] != 0 {
		t.Errorf("a failed payment credited the merchant: %d", wRepo.balances[merchantID])
	}
	// A retry must be possible.
	if _, err := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodVADummy, nil); err != nil {
		t.Errorf("a new intent should be allowed after a failed payment: %v", err)
	}
}

// An invoice can only be paid once, even via two separate pending intents.
func TestPaymentService_Process_SecondIntentCannotRepayPaidInvoice(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	merchantID := uuid.New()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)
	ctx := context.Background()

	inv := seedPendingInvoice(invRepo, merchantID, 3000)
	first, _ := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodVADummy, nil)
	second, _ := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodVADummy, nil)

	if _, err := svc.Process(ctx, first.ID, true); err != nil {
		t.Fatalf("first Process: %v", err)
	}
	if _, err := svc.Process(ctx, second.ID, true); err == nil {
		t.Fatal("the second intent paid an already-PAID invoice")
	}
	// The merchant must have been credited exactly once.
	if got := wRepo.balances[merchantID]; got != 3000 {
		t.Errorf("balance = %d, want a single 3000 credit", got)
	}
}

// SRS §3.4: payment SUCCESS is atomic — if the wallet credit fails, the invoice
// must not be left marked PAID.
func TestPaymentService_Process_RollsBackWhenWalletMissing(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	merchantID := uuid.New()
	// No wallet row for this merchant → AddBalance returns ErrNotFound.
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{}}
	svc := NewPaymentService(invRepo, piRepo, wRepo, snapshotUoW{invoices: invRepo, intents: piRepo})
	ctx := context.Background()

	inv := seedPendingInvoice(invRepo, merchantID, 3000)
	intent, err := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodVADummy, nil)
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	if _, err := svc.Process(ctx, intent.ID, true); err == nil {
		t.Fatal("expected Process to fail when the merchant wallet is missing")
	}
	if got := invRepo.store[inv.ID].Status; got != constant.InvoicePending {
		t.Errorf("invoice status = %s, want it rolled back to PENDING", got)
	}
	if got := piRepo.store[intent.ID].Status; got != constant.PaymentPending {
		t.Errorf("intent status = %s, want it rolled back to PENDING", got)
	}
}

// A WALLET payment by a logged-in payer without funds must fail rather than
// letting the balance go negative.
func TestPaymentService_Process_WalletPayerWithoutFundsFails(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	merchantID := uuid.New()
	payerID := uuid.New()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 0, payerID: 100}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)
	ctx := context.Background()

	inv := seedPendingInvoice(invRepo, merchantID, 3000)
	intent, err := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodWallet, &payerID)
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	if _, err := svc.Process(ctx, intent.ID, true); !errors.Is(err, apperror.ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
	if wRepo.balances[payerID] != 100 {
		t.Errorf("payer balance = %d, want it untouched at 100", wRepo.balances[payerID])
	}
}

// A WALLET payment moves the amount from payer to merchant — no money created.
func TestPaymentService_Process_WalletTransferConservesTotal(t *testing.T) {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	merchantID := uuid.New()
	payerID := uuid.New()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 500, payerID: 5000}}
	svc := newPaymentSvc(invRepo, piRepo, wRepo)
	ctx := context.Background()

	totalBefore := wRepo.balances[merchantID] + wRepo.balances[payerID]

	inv := seedPendingInvoice(invRepo, merchantID, 3000)
	intent, err := svc.CreateIntent(ctx, inv.PaymentToken, constant.PaymentMethodWallet, &payerID)
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if _, err := svc.Process(ctx, intent.ID, true); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if got := wRepo.balances[merchantID]; got != 3500 {
		t.Errorf("merchant balance = %d, want 3500", got)
	}
	if got := wRepo.balances[payerID]; got != 2000 {
		t.Errorf("payer balance = %d, want 2000", got)
	}
	if total := wRepo.balances[merchantID] + wRepo.balances[payerID]; total != totalBefore {
		t.Errorf("total balance changed from %d to %d — a wallet transfer must conserve value", totalBefore, total)
	}
}

// snapshotUoW restores invoice and intent state when fn fails, so tests can assert
// the rollback semantics a real DB transaction would provide.
type snapshotUoW struct {
	invoices *mockInvoiceRepo
	intents  *mockPaymentIntentRepo
}

func (u snapshotUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	invSnap := map[uuid.UUID]model.Invoice{}
	for id, inv := range u.invoices.store {
		invSnap[id] = *inv
	}
	piSnap := map[uuid.UUID]model.PaymentIntent{}
	for id, pi := range u.intents.store {
		piSnap[id] = *pi
	}

	if err := fn(ctx); err != nil {
		for id, inv := range invSnap {
			restored := inv
			u.invoices.store[id] = &restored
		}
		for id, pi := range piSnap {
			restored := pi
			u.intents.store[id] = &restored
		}
		return err
	}
	return nil
}
