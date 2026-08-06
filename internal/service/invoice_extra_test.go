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
)

// SRS §2.3: invoice_number must be unique. The generated value races against a DB
// UNIQUE constraint, so a collision has to be retried, not reported as a 500.

func TestInvoiceService_Create_RetriesOnDuplicate(t *testing.T) {
	repo := newMockInvoiceRepo()
	// The first insert collides, the second succeeds.
	repo.createErrs = []error{
		apperror.New(apperror.KindConflict, "DUPLICATE", "invoice_number already exists"),
		nil,
	}
	svc := newInvoiceSvc(repo)

	inv, err := svc.Create(context.Background(), validCreateInput(uuid.New()))
	if err != nil {
		t.Fatalf("Create should have retried past the collision: %v", err)
	}
	if repo.createCalls != 2 {
		t.Errorf("Create called the repo %d times, want 2 (one retry)", repo.createCalls)
	}
	if inv.InvoiceNumber == "" || inv.PaymentToken == "" {
		t.Error("the retried invoice must still carry a number and token")
	}
}

// The retry is bounded: a permanently colliding insert must surface an error
// rather than looping forever.
func TestInvoiceService_Create_GivesUpAfterRepeatedDuplicates(t *testing.T) {
	repo := newMockInvoiceRepo()
	dup := apperror.New(apperror.KindConflict, "DUPLICATE", "invoice_number already exists")
	repo.createErrs = []error{dup, dup, dup, dup, dup}
	svc := newInvoiceSvc(repo)

	_, err := svc.Create(context.Background(), validCreateInput(uuid.New()))
	if err == nil {
		t.Fatal("Create should fail when every attempt collides")
	}
	if repo.createCalls != createInvoiceMaxAttempts {
		t.Errorf("Create attempted %d times, want %d", repo.createCalls, createInvoiceMaxAttempts)
	}
	if code := errorCode(err); code != "INVOICE_NUMBER_COLLISION" {
		t.Errorf("error code = %q, want INVOICE_NUMBER_COLLISION", code)
	}
}

// A non-duplicate failure must not be retried — retrying a real DB outage is pointless.
func TestInvoiceService_Create_DoesNotRetryOtherErrors(t *testing.T) {
	repo := newMockInvoiceRepo()
	boom := errors.New("connection reset")
	repo.createErrs = []error{boom, nil}
	svc := newInvoiceSvc(repo)

	_, err := svc.Create(context.Background(), validCreateInput(uuid.New()))
	if !errors.Is(err, boom) {
		t.Fatalf("want the original error, got %v", err)
	}
	if repo.createCalls != 1 {
		t.Errorf("Create called the repo %d times, want exactly 1 (no retry)", repo.createCalls)
	}
}

// Each invoice gets its own number and token.
func TestInvoiceService_Create_GeneratesDistinctIdentifiers(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)
	merchantID := uuid.New()

	numbers := map[string]struct{}{}
	tokens := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		inv, err := svc.Create(context.Background(), validCreateInput(merchantID))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, dup := numbers[inv.InvoiceNumber]; dup {
			t.Fatalf("duplicate invoice number %q", inv.InvoiceNumber)
		}
		if _, dup := tokens[inv.PaymentToken]; dup {
			t.Fatalf("duplicate payment token %q", inv.PaymentToken)
		}
		numbers[inv.InvoiceNumber] = struct{}{}
		tokens[inv.PaymentToken] = struct{}{}
	}
}

// SRS §3.3: the payment token must be unguessable, so it needs real entropy.
func TestInvoiceService_Create_PaymentTokenIsLongHex(t *testing.T) {
	svc := newInvoiceSvc(newMockInvoiceRepo())

	inv, err := svc.Create(context.Background(), validCreateInput(uuid.New()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(inv.PaymentToken) != 64 { // 32 random bytes, hex-encoded
		t.Errorf("payment token length = %d, want 64 hex chars", len(inv.PaymentToken))
	}
}

// A due date exactly now is already past by the time the check runs; a comfortable
// future date must be accepted.
func TestInvoiceService_Create_DueDateBoundaries(t *testing.T) {
	svc := newInvoiceSvc(newMockInvoiceRepo())

	in := validCreateInput(uuid.New())
	in.DueDate = time.Now().Add(time.Second)
	if _, err := svc.Create(context.Background(), in); err != nil {
		t.Errorf("a due date one second in the future should be accepted: %v", err)
	}

	in.DueDate = time.Now().Add(-time.Second)
	if _, err := svc.Create(context.Background(), in); err == nil {
		t.Error("a due date one second in the past must be rejected")
	} else if code := errorCode(err); code != "INVALID_DUE_DATE" {
		t.Errorf("error code = %q, want INVALID_DUE_DATE", code)
	}
}

// SRS §2.4: the sweeper only expires overdue PENDING invoices; PAID ones are terminal.
func TestInvoiceService_ExpireDue_LeavesPaidAndExpiredAlone(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)
	merchantID := uuid.New()
	past := time.Now().Add(-time.Hour)

	overdue := seedInvoice(repo, merchantID, constant.InvoicePending, past)
	paid := seedInvoice(repo, merchantID, constant.InvoicePaid, past)
	alreadyExpired := seedInvoice(repo, merchantID, constant.InvoiceExpired, past)
	future := seedInvoice(repo, merchantID, constant.InvoicePending, time.Now().Add(time.Hour))

	res, err := svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if res.InvoicesExpired != 1 {
		t.Fatalf("expired %d invoices, want exactly 1", res.InvoicesExpired)
	}
	if repo.store[overdue].Status != constant.InvoiceExpired {
		t.Error("the overdue PENDING invoice was not expired")
	}
	if repo.store[paid].Status != constant.InvoicePaid {
		t.Error("a PAID invoice must never be expired, even when overdue")
	}
	if repo.store[alreadyExpired].Status != constant.InvoiceExpired {
		t.Error("an already-EXPIRED invoice changed")
	}
	if repo.store[future].Status != constant.InvoicePending {
		t.Error("an invoice that is not yet due was expired")
	}
}

// Running the sweeper twice must be a no-op the second time.
func TestInvoiceService_ExpireDue_IsIdempotent(t *testing.T) {
	repo := newMockInvoiceRepo()
	svc := newInvoiceSvc(repo)
	seedInvoice(repo, uuid.New(), constant.InvoicePending, time.Now().Add(-time.Hour))

	if res, err := svc.ExpireDue(context.Background()); err != nil || res.InvoicesExpired != 1 {
		t.Fatalf("first sweep: expired=%d err=%v", res.InvoicesExpired, err)
	}
	if res, err := svc.ExpireDue(context.Background()); err != nil || res.InvoicesExpired != 0 {
		t.Fatalf("second sweep should be a no-op: expired=%d err=%v", res.InvoicesExpired, err)
	}
}

// seedInvoice inserts an invoice directly into the mock and returns its id.
func seedInvoice(repo *mockInvoiceRepo, merchantID uuid.UUID, status constant.InvoiceStatus, dueDate time.Time) uuid.UUID {
	id := uuid.New()
	repo.store[id] = &model.Invoice{
		ID:         id,
		MerchantID: merchantID,
		Amount:     1000,
		Status:     status,
		DueDate:    dueDate,
	}
	return id
}
