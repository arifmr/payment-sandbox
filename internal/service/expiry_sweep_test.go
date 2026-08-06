package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/repository"
)

// The expiry sweeper runs as an in-process goroutine, so every replica fires one on
// every tick. An advisory lock elects a single sweeper per tick, and the sweep also
// settles the payment intents its invoices leave behind.

// countingLocker records how often a lock was requested and whether it grants.
type countingLocker struct {
	grant bool
	err   error
	calls int
	key   repository.AdvisoryLockKey
}

func (l *countingLocker) TryLockTx(_ context.Context, key repository.AdvisoryLockKey) (bool, error) {
	l.calls++
	l.key = key
	if l.err != nil {
		return false, l.err
	}
	return l.grant, nil
}

var _ repository.AdvisoryLocker = (*countingLocker)(nil)

// sweepEnv wires an invoice repo and an intent repo that share invoice-status knowledge,
// which is what FailPendingForExpiredInvoices needs to see.
type sweepEnv struct {
	invoices *mockInvoiceRepo
	intents  *mockPaymentIntentRepo
	locker   *countingLocker
	svc      InvoiceService
}

func newSweepEnv(grant bool) *sweepEnv {
	invRepo := newMockInvoiceRepo()
	piRepo := newMockPaymentIntentRepo()
	// Wiring the invoice repo in is what makes the intent sweep observe live invoice
	// status, the same way the real SQL joins against the invoices table.
	piRepo.invoices = invRepo
	locker := &countingLocker{grant: grant}
	return &sweepEnv{
		invoices: invRepo,
		intents:  piRepo,
		locker:   locker,
		svc:      NewInvoiceService(invRepo, piRepo, locker, noopUoW{}),
	}
}

// addInvoice inserts an invoice.
func (e *sweepEnv) addInvoice(status constant.InvoiceStatus, dueDate time.Time) uuid.UUID {
	id := uuid.New()
	e.invoices.store[id] = &model.Invoice{
		ID: id, MerchantID: uuid.New(), Amount: 1000, Status: status, DueDate: dueDate,
	}
	return id
}

// addIntent attaches a payment intent to an invoice.
func (e *sweepEnv) addIntent(invoiceID uuid.UUID, status constant.PaymentIntentStatus) uuid.UUID {
	id := uuid.New()
	e.intents.store[id] = &model.PaymentIntent{
		ID: id, InvoiceID: invoiceID, Method: constant.PaymentMethodVADummy,
		Status: status, Amount: 1000,
	}
	return id
}

// ── advisory lock ─────────────────────────────────────────────────────────────

func TestExpireDue_TakesTheSweepLock(t *testing.T) {
	e := newSweepEnv(true)

	if _, err := e.svc.ExpireDue(context.Background()); err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if e.locker.calls != 1 {
		t.Errorf("lock requested %d times, want exactly 1", e.locker.calls)
	}
	if e.locker.key != repository.LockInvoiceExpirySweep {
		t.Errorf("locked key %d, want LockInvoiceExpirySweep", e.locker.key)
	}
}

// Losing the race is normal in a multi-replica deployment: the other instance is
// already sweeping, so this tick reports Skipped rather than an error.
func TestExpireDue_SkipsWhenLockHeldElsewhere(t *testing.T) {
	e := newSweepEnv(false)
	past := time.Now().Add(-time.Hour)
	overdue := e.addInvoice(constant.InvoicePending, past)

	res, err := e.svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("a contended lock must not be an error: %v", err)
	}
	if !res.Skipped {
		t.Error("Skipped must be true when the lock was not acquired")
	}
	if res.InvoicesExpired != 0 || res.IntentsFailed != 0 {
		t.Errorf("a skipped sweep must not report work: %+v", res)
	}
	// Crucially, no work was actually done — the other replica owns this tick.
	if got := e.invoices.store[overdue].Status; got != constant.InvoicePending {
		t.Errorf("invoice status = %s, want it untouched at PENDING", got)
	}
}

// A lock failure is a real error (the database is unreachable), distinct from losing
// the race, and must be reported rather than silently skipped.
func TestExpireDue_LockErrorIsPropagated(t *testing.T) {
	e := newSweepEnv(true)
	boom := errors.New("connection reset")
	e.locker.err = boom

	res, err := e.svc.ExpireDue(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("want the underlying error, got %v", err)
	}
	if res.Skipped {
		t.Error("a lock error is not a skip")
	}
}

// ── intent settlement ─────────────────────────────────────────────────────────

// An intent whose invoice became EXPIRED can never legally reach SUCCESS, so leaving
// it PENDING would understate total_failed on the §2.6 dashboard forever.
func TestExpireDue_SettlesIntentsOfNewlyExpiredInvoices(t *testing.T) {
	e := newSweepEnv(true)
	past := time.Now().Add(-time.Hour)

	overdue := e.addInvoice(constant.InvoicePending, past)
	pending := e.addIntent(overdue, constant.PaymentPending)

	res, err := e.svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if res.InvoicesExpired != 1 {
		t.Errorf("invoices expired = %d, want 1", res.InvoicesExpired)
	}
	if res.IntentsFailed != 1 {
		t.Errorf("intents failed = %d, want 1", res.IntentsFailed)
	}
	if got := e.intents.store[pending].Status; got != constant.PaymentFailed {
		t.Errorf("intent status = %s, want FAILED", got)
	}
	if e.intents.store[pending].ProcessedAt == nil {
		t.Error("processed_at must be set once the intent is settled")
	}
}

// Only PENDING intents of EXPIRED invoices are touched. Everything else is left alone.
func TestExpireDue_LeavesUnrelatedIntentsAlone(t *testing.T) {
	e := newSweepEnv(true)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	// Will expire this sweep.
	overdue := e.addInvoice(constant.InvoicePending, past)
	targeted := e.addIntent(overdue, constant.PaymentPending)

	// Already-settled intent on the same expiring invoice: terminal, must not change.
	settled := e.addIntent(overdue, constant.PaymentSuccess)

	// Intent on an invoice that is not due yet.
	live := e.addInvoice(constant.InvoicePending, future)
	liveIntent := e.addIntent(live, constant.PaymentPending)

	// Intent on a PAID invoice.
	paid := e.addInvoice(constant.InvoicePaid, past)
	paidIntent := e.addIntent(paid, constant.PaymentPending)

	res, err := e.svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if res.IntentsFailed != 1 {
		t.Errorf("intents failed = %d, want exactly 1", res.IntentsFailed)
	}

	for name, tc := range map[string]struct {
		id   uuid.UUID
		want constant.PaymentIntentStatus
	}{
		"intent of the expired invoice":   {targeted, constant.PaymentFailed},
		"already SUCCESS intent":          {settled, constant.PaymentSuccess},
		"intent of a not-yet-due invoice": {liveIntent, constant.PaymentPending},
		"intent of a PAID invoice":        {paidIntent, constant.PaymentPending},
	} {
		if got := e.intents.store[tc.id].Status; got != tc.want {
			t.Errorf("%s: status = %s, want %s", name, got, tc.want)
		}
	}
}

// Ordering matters: the intent sweep looks for invoices already EXPIRED, so it has to
// run after MarkExpired to observe this sweep's own work. If the order were reversed,
// intents would always lag one tick behind.
func TestExpireDue_IntentSweepSeesThisSweepsExpirations(t *testing.T) {
	e := newSweepEnv(true)
	past := time.Now().Add(-time.Hour)

	// The invoice is PENDING at the start of the sweep, not already EXPIRED. The intent
	// mock reads invoice status live, so it can only match this intent if MarkExpired
	// has already run — which is exactly the ordering under test.
	overdue := e.addInvoice(constant.InvoicePending, past)
	intent := e.addIntent(overdue, constant.PaymentPending)

	res, err := e.svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if res.IntentsFailed != 1 {
		t.Fatalf("intents failed = %d, want 1 — the intent sweep ran before MarkExpired", res.IntentsFailed)
	}
	if got := e.intents.store[intent].Status; got != constant.PaymentFailed {
		t.Errorf("intent status = %s, want FAILED in the same sweep", got)
	}
}

// Nothing to do is a valid outcome and must not look like a skip.
func TestExpireDue_NoWorkIsNotASkip(t *testing.T) {
	e := newSweepEnv(true)
	e.addInvoice(constant.InvoicePending, time.Now().Add(time.Hour)) // not due

	res, err := e.svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if res.Skipped {
		t.Error("an idle sweep is not a skipped sweep")
	}
	if res.InvoicesExpired != 0 || res.IntentsFailed != 0 {
		t.Errorf("expected no work, got %+v", res)
	}
}
