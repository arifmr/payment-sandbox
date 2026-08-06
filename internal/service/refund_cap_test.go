package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
)

// SRS §2.5 / §3.4: a refund draws against the invoice. The cap must consider every
// refund that still holds a claim, not just the amount of the request in hand —
// otherwise N partial refunds of the full amount drain the merchant's wallet.

func newRefundEnv(t *testing.T, invoiceAmount int64) (*mockRefundRepo, *mockInvoiceRepo, *mockPaymentIntentRepo, *mockWalletRepo, RefundService, uuid.UUID, *model.Invoice) {
	t.Helper()

	merchantID := uuid.New()
	rfRepo := newMockRefundRepo()
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 1_000_000}}

	inv, _ := seedPaidInvoiceWithIntent(invRepo, piRepo, merchantID, invoiceAmount)
	svc := newRefundSvc(rfRepo, invRepo, piRepo, wRepo)

	return rfRepo, invRepo, piRepo, wRepo, svc, merchantID, inv
}

func TestRefundService_Request_PartialRefundsCannotExceedInvoice(t *testing.T) {
	_, _, _, _, svc, merchantID, inv := newRefundEnv(t, 1000)
	ctx := context.Background()

	// Two 400 refunds fit inside the 1000 invoice.
	if _, err := svc.Request(ctx, merchantID, inv.ID, 400, "partial 1"); err != nil {
		t.Fatalf("first partial refund: %v", err)
	}
	if _, err := svc.Request(ctx, merchantID, inv.ID, 400, "partial 2"); err != nil {
		t.Fatalf("second partial refund: %v", err)
	}

	// A third 400 would total 1200 > 1000 and must be refused.
	if _, err := svc.Request(ctx, merchantID, inv.ID, 400, "partial 3"); err == nil {
		t.Fatal("third partial refund was accepted, over-refunding the invoice")
	}

	// The exact remainder is still allowed.
	if _, err := svc.Request(ctx, merchantID, inv.ID, 200, "remainder"); err != nil {
		t.Fatalf("refunding the exact remaining balance failed: %v", err)
	}

	// And nothing more.
	if _, err := svc.Request(ctx, merchantID, inv.ID, 1, "one too many"); err == nil {
		t.Fatal("a fully refunded invoice accepted another refund")
	}
}

func TestRefundService_Request_FullRefundAllowedOnce(t *testing.T) {
	_, _, _, _, svc, merchantID, inv := newRefundEnv(t, 1000)
	ctx := context.Background()

	if _, err := svc.Request(ctx, merchantID, inv.ID, 1000, "full"); err != nil {
		t.Fatalf("full refund: %v", err)
	}
	if _, err := svc.Request(ctx, merchantID, inv.ID, 1000, "full again"); err == nil {
		t.Fatal("the invoice was refunded twice in full")
	}
}

func TestRefundService_Request_SingleRefundOverInvoiceIsRefused(t *testing.T) {
	_, _, _, _, svc, merchantID, inv := newRefundEnv(t, 1000)

	if _, err := svc.Request(context.Background(), merchantID, inv.ID, 1001, "too much"); err == nil {
		t.Fatal("a refund larger than the invoice was accepted")
	}
}

// A REJECTED or FAILED refund releases its claim, so the amount becomes available again.
func TestRefundService_Request_RejectedRefundReleasesItsClaim(t *testing.T) {
	rfRepo, _, _, _, svc, merchantID, inv := newRefundEnv(t, 1000)
	ctx := context.Background()

	rf, err := svc.Request(ctx, merchantID, inv.ID, 1000, "full")
	if err != nil {
		t.Fatalf("first refund: %v", err)
	}

	// While REQUESTED it holds the whole invoice.
	if _, err := svc.Request(ctx, merchantID, inv.ID, 1, "should not fit"); err == nil {
		t.Fatal("an in-flight refund did not reserve the invoice amount")
	}

	// Reject it, then the amount is free again.
	if _, err := svc.AdminAction(ctx, rf.ID, RefundActionReject); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rfRepo.store[rf.ID].Status != constant.RefundRejected {
		t.Fatalf("status = %s, want REJECTED", rfRepo.store[rf.ID].Status)
	}
	if _, err := svc.Request(ctx, merchantID, inv.ID, 1000, "retry after rejection"); err != nil {
		t.Fatalf("refund after rejection should be allowed: %v", err)
	}
}

// A FAILED refund likewise frees the amount for a retry.
func TestRefundService_Request_FailedRefundReleasesItsClaim(t *testing.T) {
	_, _, _, _, svc, merchantID, inv := newRefundEnv(t, 500)
	ctx := context.Background()

	rf, err := svc.Request(ctx, merchantID, inv.ID, 500, "attempt")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := svc.AdminAction(ctx, rf.ID, RefundActionApprove); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := svc.AdminAction(ctx, rf.ID, RefundActionFail); err != nil {
		t.Fatalf("fail: %v", err)
	}

	if _, err := svc.Request(ctx, merchantID, inv.ID, 500, "retry after failure"); err != nil {
		t.Fatalf("refund after a failed attempt should be allowed: %v", err)
	}
}

// A SUCCESS refund keeps its claim forever — money already left the wallet.
func TestRefundService_Request_SuccessfulRefundKeepsItsClaim(t *testing.T) {
	_, _, _, wRepo, svc, merchantID, inv := newRefundEnv(t, 1000)
	ctx := context.Background()

	rf, err := svc.Request(ctx, merchantID, inv.ID, 600, "partial")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := svc.AdminAction(ctx, rf.ID, RefundActionApprove); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := svc.AdminAction(ctx, rf.ID, RefundActionProcess); err != nil {
		t.Fatalf("process: %v", err)
	}
	if got := wRepo.balances[merchantID]; got != 1_000_000-600 {
		t.Fatalf("balance = %d, want %d", got, 1_000_000-600)
	}

	// Only 400 remains.
	if _, err := svc.Request(ctx, merchantID, inv.ID, 401, "over the remainder"); err == nil {
		t.Fatal("refund exceeded the remaining balance after a successful payout")
	}
	if _, err := svc.Request(ctx, merchantID, inv.ID, 400, "the remainder"); err != nil {
		t.Fatalf("refunding the exact remainder failed: %v", err)
	}
}

// Refunds on other invoices must not consume this invoice's allowance.
func TestRefundService_Request_CapIsPerInvoice(t *testing.T) {
	rfRepo, invRepo, piRepo, wRepo, svc, merchantID, first := newRefundEnv(t, 1000)
	second, _ := seedPaidInvoiceWithIntent(invRepo, piRepo, merchantID, 1000)
	_, _ = rfRepo, wRepo
	ctx := context.Background()

	if _, err := svc.Request(ctx, merchantID, first.ID, 1000, "first invoice"); err != nil {
		t.Fatalf("first invoice refund: %v", err)
	}
	// The second invoice still has its own full allowance.
	if _, err := svc.Request(ctx, merchantID, second.ID, 1000, "second invoice"); err != nil {
		t.Fatalf("second invoice refund should be independent: %v", err)
	}
}

// SRS §3.4: the read-then-insert must happen under a row lock, so Request has to
// take the invoice FOR UPDATE rather than doing a plain read.
func TestRefundService_Request_LocksInvoiceRow(t *testing.T) {
	_, invRepo, _, _, svc, merchantID, inv := newRefundEnv(t, 1000)

	if _, err := svc.Request(context.Background(), merchantID, inv.ID, 100, "reason"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if invRepo.lockedForUpdate == 0 {
		t.Error("Request must read the invoice FOR UPDATE to serialize concurrent requests")
	}
}

// An invoice with no successful payment has nothing to refund, and the error must
// be a clear 422 rather than a bare NOT_FOUND from the intent lookup.
func TestRefundService_Request_NoSuccessfulPaymentIsUnprocessable(t *testing.T) {
	merchantID := uuid.New()
	invRepo := newMockInvoiceRepo()
	inv := &model.Invoice{
		ID: uuid.New(), MerchantID: merchantID, Amount: 1000, Status: constant.InvoicePaid,
	}
	invRepo.store[inv.ID] = inv

	svc := newRefundSvc(newMockRefundRepo(), invRepo, newMockPaymentIntentRepo(),
		&mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}})

	_, err := svc.Request(context.Background(), merchantID, inv.ID, 100, "reason")
	if err == nil {
		t.Fatal("expected an error when the invoice has no successful payment")
	}
	if code := errorCode(err); code != "NO_SUCCESSFUL_PAYMENT" {
		t.Errorf("error code = %q, want NO_SUCCESSFUL_PAYMENT", code)
	}
}

// ── AdminAction transition coverage ───────────────────────────────────────────

// Every illegal edge of the refund state machine must be refused.
func TestRefundService_AdminAction_RejectsEveryIllegalTransition(t *testing.T) {
	cases := []struct {
		from   constant.RefundStatus
		action RefundAction
	}{
		{constant.RefundRequested, RefundActionProcess}, // needs APPROVE first
		{constant.RefundRequested, RefundActionFail},
		{constant.RefundApproved, RefundActionApprove}, // already approved
		{constant.RefundApproved, RefundActionReject},
		{constant.RefundRejected, RefundActionApprove}, // terminal
		{constant.RefundRejected, RefundActionProcess},
		{constant.RefundSuccess, RefundActionProcess}, // terminal
		{constant.RefundSuccess, RefundActionFail},
		{constant.RefundFailed, RefundActionProcess}, // terminal
		{constant.RefundFailed, RefundActionApprove},
	}

	for _, tc := range cases {
		t.Run(string(tc.from)+"->"+string(tc.action), func(t *testing.T) {
			merchantID := uuid.New()
			rid := uuid.New()
			rfRepo := newMockRefundRepo()
			rfRepo.store[rid] = &model.Refund{ID: rid, MerchantID: merchantID, Amount: 100, Status: tc.from}
			wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 10_000}}
			svc := NewRefundService(rfRepo, nil, nil, wRepo, noopUoW{})

			if _, err := svc.AdminAction(context.Background(), rid, tc.action); err == nil {
				t.Fatalf("%s -> %s was accepted", tc.from, tc.action)
			}
			// The wallet must be untouched by a rejected transition.
			if wRepo.balances[merchantID] != 10_000 {
				t.Errorf("wallet changed on an illegal transition: %d", wRepo.balances[merchantID])
			}
		})
	}
}

// APPROVE is not terminal, so it must leave processed_at unset.
func TestRefundService_AdminAction_ApproveDoesNotSetProcessedAt(t *testing.T) {
	merchantID := uuid.New()
	rid := uuid.New()
	rfRepo := newMockRefundRepo()
	rfRepo.store[rid] = &model.Refund{ID: rid, MerchantID: merchantID, Amount: 100, Status: constant.RefundRequested}
	svc := NewRefundService(rfRepo, nil, nil, &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 500}}, noopUoW{})

	rf, err := svc.AdminAction(context.Background(), rid, RefundActionApprove)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if rf.ProcessedAt != nil {
		t.Error("processed_at must stay nil until the refund reaches a terminal state")
	}
}

// APPROVE alone must not move money; only PROCESS does.
func TestRefundService_AdminAction_ApproveDoesNotTouchWallet(t *testing.T) {
	merchantID := uuid.New()
	rid := uuid.New()
	rfRepo := newMockRefundRepo()
	rfRepo.store[rid] = &model.Refund{ID: rid, MerchantID: merchantID, Amount: 700, Status: constant.RefundRequested}
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}}
	svc := NewRefundService(rfRepo, nil, nil, wRepo, noopUoW{})

	if _, err := svc.AdminAction(context.Background(), rid, RefundActionApprove); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if wRepo.balances[merchantID] != 5000 {
		t.Errorf("balance = %d, want it unchanged at 5000", wRepo.balances[merchantID])
	}
	if wRepo.debited {
		t.Error("APPROVE must not debit the wallet")
	}
}

// SRS §3.4: refund SUCCESS is atomic. If the wallet debit fails, the status change
// must not be observable — the UoW rolls the whole thing back.
func TestRefundService_AdminAction_ProcessRollsBackWhenWalletDebitFails(t *testing.T) {
	merchantID := uuid.New()
	rid := uuid.New()
	rfRepo := newMockRefundRepo()
	rfRepo.store[rid] = &model.Refund{ID: rid, MerchantID: merchantID, Amount: 5000, Status: constant.RefundApproved}
	// Balance is too small to absorb the refund.
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 100}}

	svc := NewRefundService(rfRepo, nil, nil, wRepo, rollbackUoW{store: rfRepo, id: rid})

	if _, err := svc.AdminAction(context.Background(), rid, RefundActionProcess); err == nil {
		t.Fatal("expected the refund to fail when the wallet cannot be debited")
	}
	if got := rfRepo.store[rid].Status; got != constant.RefundApproved {
		t.Errorf("status = %s, want it rolled back to APPROVED", got)
	}
	if wRepo.balances[merchantID] != 100 {
		t.Errorf("balance = %d, want it unchanged at 100", wRepo.balances[merchantID])
	}
}

// FAIL is terminal and must record processed_at without moving money.
func TestRefundService_AdminAction_FailIsTerminalWithoutDebit(t *testing.T) {
	merchantID := uuid.New()
	rid := uuid.New()
	rfRepo := newMockRefundRepo()
	rfRepo.store[rid] = &model.Refund{ID: rid, MerchantID: merchantID, Amount: 300, Status: constant.RefundApproved}
	wRepo := &mockWalletRepo{balances: map[uuid.UUID]int64{merchantID: 5000}}
	svc := NewRefundService(rfRepo, nil, nil, wRepo, noopUoW{})

	rf, err := svc.AdminAction(context.Background(), rid, RefundActionFail)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if rf.Status != constant.RefundFailed || rf.ProcessedAt == nil {
		t.Errorf("unexpected refund state: status=%s processedAt=%v", rf.Status, rf.ProcessedAt)
	}
	if wRepo.balances[merchantID] != 5000 {
		t.Errorf("FAIL must not debit the wallet, balance = %d", wRepo.balances[merchantID])
	}
}

func TestRefundAction_Valid(t *testing.T) {
	for _, a := range []RefundAction{RefundActionApprove, RefundActionReject, RefundActionProcess, RefundActionFail} {
		if !a.Valid() {
			t.Errorf("RefundAction(%q) must be valid", a)
		}
	}
	for _, a := range []RefundAction{"", "SUCCESS", "approve", "CANCEL"} {
		if a.Valid() {
			t.Errorf("RefundAction(%q) must not be valid", a)
		}
	}
}

// rollbackUoW simulates a real transaction: when fn fails, mutations made through
// the mocks are undone so the test can assert the rollback is observable.
type rollbackUoW struct {
	store *mockRefundRepo
	id    uuid.UUID
}

func (u rollbackUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	before := *u.store.store[u.id]
	if err := fn(ctx); err != nil {
		restored := before
		u.store.store[u.id] = &restored
		return err
	}
	return nil
}
