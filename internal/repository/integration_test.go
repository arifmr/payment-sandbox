//go:build integration

// Integration tests for the repository layer and the Unit of Work. These exercise
// the parts that unit tests with in-memory mocks cannot prove: the actual SQL, the
// CAS-style status updates, the non-negative balance guard, and real transaction
// rollback.
//
// They need a live Postgres:
//
//	docker compose up -d postgres
//	export TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/payment_sandbox?sslmode=disable'
//	go test -tags=integration ./internal/repository/
//
// Every test works inside its own schema-scoped data and cleans up after itself.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

var (
	testDB     *sql.DB
	testDBOnce sync.Once
)

// openTestDB connects once per package run and applies migrations.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	testDBOnce.Do(func() {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			return
		}
		db, err := OpenDB(dsn)
		if err != nil {
			t.Fatalf("connecting to TEST_DATABASE_URL: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := Migrate(ctx, db); err != nil {
			t.Fatalf("migrating test database: %v", err)
		}
		testDB = db
	})

	if testDB == nil {
		t.Skip("TEST_DATABASE_URL is not set; skipping repository integration tests")
	}
	return testDB
}

// seedMerchant creates a merchant with a wallet and registers cleanup. Deleting the
// user cascades to wallets, invoices, intents and refunds.
func seedMerchant(t *testing.T, db *sql.DB, balance int64) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	id := uuid.New()
	if err := NewUserRepo(db).Create(ctx, &model.User{
		ID:           id,
		Email:        id.String() + "@test.local",
		PasswordHash: "x",
		Name:         "Test Merchant",
		Role:         constant.RoleMerchant,
	}); err != nil {
		t.Fatalf("creating merchant: %v", err)
	}
	if err := NewWalletRepo(db).Create(ctx, &model.Wallet{
		ID: uuid.New(), MerchantID: id, Balance: balance,
	}); err != nil {
		t.Fatalf("creating wallet: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func seedInvoice(t *testing.T, db *sql.DB, merchantID uuid.UUID, amount int64, status constant.InvoiceStatus, dueDate time.Time) *model.Invoice {
	t.Helper()

	inv := &model.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: "INV-TEST-" + uuid.NewString()[:12],
		MerchantID:    merchantID,
		CustomerName:  "Budi",
		Amount:        amount,
		Status:        status,
		DueDate:       dueDate,
		PaymentToken:  uuid.NewString() + uuid.NewString(),
	}
	if err := NewInvoiceRepo(db).Create(context.Background(), inv); err != nil {
		t.Fatalf("creating invoice: %v", err)
	}
	return inv
}

func balanceOf(t *testing.T, db *sql.DB, merchantID uuid.UUID) int64 {
	t.Helper()
	w, err := NewWalletRepo(db).FindByMerchantID(context.Background(), merchantID)
	if err != nil {
		t.Fatalf("reading balance: %v", err)
	}
	return w.Balance
}

// ── unique constraints (SRS §2.3 / §3.3) ──────────────────────────────────────

// A duplicate invoice_number must surface as a domain CONFLICT, not a raw driver
// error, so the service can retry it instead of returning a 500.
func TestIntegration_InvoiceRepo_DuplicateNumberIsConflict(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewInvoiceRepo(db)
	ctx := context.Background()

	first := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, time.Now().Add(time.Hour))

	clash := &model.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: first.InvoiceNumber, // same number
		MerchantID:    merchantID,
		Amount:        2000,
		Status:        constant.InvoicePending,
		DueDate:       time.Now().Add(time.Hour),
		PaymentToken:  uuid.NewString() + uuid.NewString(),
	}
	err := repo.Create(ctx, clash)
	if err == nil {
		t.Fatal("a duplicate invoice_number was accepted")
	}
	if !apperror.IsKind(err, apperror.KindConflict) {
		t.Errorf("error kind is not CONFLICT: %v", err)
	}
}

func TestIntegration_InvoiceRepo_DuplicatePaymentTokenIsConflict(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	ctx := context.Background()

	first := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, time.Now().Add(time.Hour))

	err := NewInvoiceRepo(db).Create(ctx, &model.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: "INV-TEST-" + uuid.NewString()[:12],
		MerchantID:    merchantID,
		Amount:        1000,
		Status:        constant.InvoicePending,
		DueDate:       time.Now().Add(time.Hour),
		PaymentToken:  first.PaymentToken, // same token
	})
	if !apperror.IsKind(err, apperror.KindConflict) {
		t.Errorf("duplicate payment_token: want a CONFLICT, got %v", err)
	}
}

func TestIntegration_UserRepo_DuplicateEmailIsConflict(t *testing.T) {
	db := openTestDB(t)
	repo := NewUserRepo(db)
	ctx := context.Background()

	id := seedMerchant(t, db, 0)
	existing, err := repo.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}

	err = repo.Create(ctx, &model.User{
		ID: uuid.New(), Email: existing.Email, PasswordHash: "x", Name: "Copy", Role: constant.RoleMerchant,
	})
	if !apperror.IsKind(err, apperror.KindConflict) {
		t.Errorf("duplicate email: want a CONFLICT, got %v", err)
	}
}

// ── CAS status transitions (SRS §3.4) ─────────────────────────────────────────

// UpdateStatus carries `AND status = <from>`, so a stale transition must affect
// zero rows and report INVALID_STATE rather than silently overwriting.
func TestIntegration_InvoiceRepo_UpdateStatusIsCompareAndSwap(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewInvoiceRepo(db)
	ctx := context.Background()

	inv := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, time.Now().Add(time.Hour))
	paidAt := time.Now().UTC()

	if err := repo.UpdateStatus(ctx, inv.ID, constant.InvoicePending, constant.InvoicePaid, &paidAt); err != nil {
		t.Fatalf("PENDING -> PAID: %v", err)
	}

	// The same transition must not apply twice.
	err := repo.UpdateStatus(ctx, inv.ID, constant.InvoicePending, constant.InvoicePaid, &paidAt)
	if !errors.Is(err, apperror.ErrInvalidState) {
		t.Errorf("replayed transition: want ErrInvalidState, got %v", err)
	}

	// A PAID invoice must not become EXPIRED.
	err = repo.UpdateStatus(ctx, inv.ID, constant.InvoicePending, constant.InvoiceExpired, nil)
	if !errors.Is(err, apperror.ErrInvalidState) {
		t.Errorf("PAID -> EXPIRED: want ErrInvalidState, got %v", err)
	}

	stored, err := repo.FindByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.Status != constant.InvoicePaid {
		t.Errorf("status = %s, want PAID", stored.Status)
	}
	if stored.PaidAt == nil {
		t.Error("paid_at was not persisted")
	}
}

func TestIntegration_TopupRepo_UpdateStatusOnlyFromPending(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewTopupRepo(db)
	ctx := context.Background()

	topup := &model.Topup{ID: uuid.New(), MerchantID: merchantID, Amount: 500, Status: constant.TopupPending}
	if err := repo.Create(ctx, topup); err != nil {
		t.Fatalf("Create: %v", err)
	}
	now := time.Now().UTC()

	if err := repo.UpdateStatus(ctx, topup.ID, constant.TopupSuccess, &now); err != nil {
		t.Fatalf("PENDING -> SUCCESS: %v", err)
	}
	if err := repo.UpdateStatus(ctx, topup.ID, constant.TopupFailed, &now); !errors.Is(err, apperror.ErrInvalidState) {
		t.Errorf("SUCCESS -> FAILED: want ErrInvalidState, got %v", err)
	}
}

// MarkExpired must touch only overdue PENDING rows.
func TestIntegration_InvoiceRepo_MarkExpiredIsSelective(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewInvoiceRepo(db)
	ctx := context.Background()
	past := time.Now().Add(-2 * time.Hour)

	overdue := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, past)
	paid := seedInvoice(t, db, merchantID, 1000, constant.InvoicePaid, past)
	future := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, time.Now().Add(2*time.Hour))

	if _, err := repo.MarkExpired(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("MarkExpired: %v", err)
	}

	for _, tc := range []struct {
		id   uuid.UUID
		name string
		want constant.InvoiceStatus
	}{
		{overdue.ID, "overdue pending", constant.InvoiceExpired},
		{paid.ID, "overdue paid", constant.InvoicePaid},
		{future.ID, "not yet due", constant.InvoicePending},
	} {
		stored, err := repo.FindByID(ctx, tc.id)
		if err != nil {
			t.Fatalf("FindByID(%s): %v", tc.name, err)
		}
		if stored.Status != tc.want {
			t.Errorf("%s: status = %s, want %s", tc.name, stored.Status, tc.want)
		}
	}
}

// ── wallet balance guard ──────────────────────────────────────────────────────

// AddBalance carries `AND balance + delta >= 0` for debits, so a wallet can never
// be driven negative.
func TestIntegration_WalletRepo_DebitCannotGoNegative(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 1000)
	repo := NewWalletRepo(db)
	ctx := context.Background()

	if err := repo.AddBalance(ctx, merchantID, -1000); err != nil {
		t.Fatalf("debiting the full balance should succeed: %v", err)
	}
	if got := balanceOf(t, db, merchantID); got != 0 {
		t.Fatalf("balance = %d, want 0", got)
	}

	if err := repo.AddBalance(ctx, merchantID, -1); !errors.Is(err, apperror.ErrInsufficientFunds) {
		t.Errorf("overdraft: want ErrInsufficientFunds, got %v", err)
	}
	if got := balanceOf(t, db, merchantID); got != 0 {
		t.Errorf("balance = %d after a refused debit, want 0", got)
	}
}

func TestIntegration_WalletRepo_MissingWalletIsNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewWalletRepo(db)

	err := repo.AddBalance(context.Background(), uuid.New(), 100)
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Errorf("want ErrNotFound for an unknown wallet, got %v", err)
	}
}

// Concurrent credits must all land — the UPDATE increments in place rather than
// writing back a stale read.
func TestIntegration_WalletRepo_ConcurrentCreditsAllApply(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewWalletRepo(db)

	const workers = 20
	const each = int64(50)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := repo.AddBalance(context.Background(), merchantID, each); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent AddBalance: %v", err)
	}

	if got, want := balanceOf(t, db, merchantID), int64(workers)*each; got != want {
		t.Errorf("balance = %d, want %d — a concurrent credit was lost", got, want)
	}
}

// ── Unit of Work (SRS §3.4) ───────────────────────────────────────────────────

// A UoW that returns an error must leave no trace of its writes.
func TestIntegration_UoW_RollsBackEveryWrite(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 1000)
	uow := transaction.NewUoW(db)
	invRepo := NewInvoiceRepo(db)
	walletRepo := NewWalletRepo(db)
	ctx := context.Background()

	inv := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, time.Now().Add(time.Hour))
	paidAt := time.Now().UTC()
	sentinel := errors.New("deliberate failure")

	err := uow.Do(ctx, func(txCtx context.Context) error {
		if err := invRepo.UpdateStatus(txCtx, inv.ID, constant.InvoicePending, constant.InvoicePaid, &paidAt); err != nil {
			return err
		}
		if err := walletRepo.AddBalance(txCtx, merchantID, 5000); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want the sentinel error back, got %v", err)
	}

	stored, err := invRepo.FindByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.Status != constant.InvoicePending {
		t.Errorf("invoice status = %s, want it rolled back to PENDING", stored.Status)
	}
	if got := balanceOf(t, db, merchantID); got != 1000 {
		t.Errorf("balance = %d, want it rolled back to 1000", got)
	}
}

func TestIntegration_UoW_CommitsOnSuccess(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 1000)
	uow := transaction.NewUoW(db)
	invRepo := NewInvoiceRepo(db)
	walletRepo := NewWalletRepo(db)
	ctx := context.Background()

	inv := seedInvoice(t, db, merchantID, 2500, constant.InvoicePending, time.Now().Add(time.Hour))
	paidAt := time.Now().UTC()

	if err := uow.Do(ctx, func(txCtx context.Context) error {
		if err := invRepo.UpdateStatus(txCtx, inv.ID, constant.InvoicePending, constant.InvoicePaid, &paidAt); err != nil {
			return err
		}
		return walletRepo.AddBalance(txCtx, merchantID, 2500)
	}); err != nil {
		t.Fatalf("UoW: %v", err)
	}

	stored, err := invRepo.FindByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.Status != constant.InvoicePaid {
		t.Errorf("invoice status = %s, want PAID", stored.Status)
	}
	if got := balanceOf(t, db, merchantID); got != 3500 {
		t.Errorf("balance = %d, want 3500", got)
	}
}

// ── refund aggregation (the over-refund guard) ────────────────────────────────

// SumOutstandingByInvoice must count SUCCESS plus in-flight refunds and exclude
// REJECTED/FAILED ones.
func TestIntegration_RefundRepo_SumOutstandingByInvoice(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 100000)
	ctx := context.Background()

	inv := seedInvoice(t, db, merchantID, 10000, constant.InvoicePaid, time.Now().Add(time.Hour))

	intent := &model.PaymentIntent{
		ID: uuid.New(), InvoiceID: inv.ID, Method: constant.PaymentMethodVADummy,
		Status: constant.PaymentSuccess, Amount: 10000,
	}
	if err := NewPaymentIntentRepo(db).Create(ctx, intent); err != nil {
		t.Fatalf("creating intent: %v", err)
	}

	refundRepo := NewRefundRepo(db)
	add := func(amount int64, status constant.RefundStatus) {
		t.Helper()
		if err := refundRepo.Create(ctx, &model.Refund{
			ID: uuid.New(), InvoiceID: inv.ID, PaymentIntentID: intent.ID,
			MerchantID: merchantID, Amount: amount, Status: status,
		}); err != nil {
			t.Fatalf("creating refund: %v", err)
		}
	}

	add(1000, constant.RefundRequested)
	add(2000, constant.RefundApproved)
	add(3000, constant.RefundSuccess)
	add(4000, constant.RefundRejected) // excluded
	add(5000, constant.RefundFailed)   // excluded

	got, err := refundRepo.SumOutstandingByInvoice(ctx, inv.ID)
	if err != nil {
		t.Fatalf("SumOutstandingByInvoice: %v", err)
	}
	if want := int64(6000); got != want {
		t.Errorf("outstanding = %d, want %d (1000 + 2000 + 3000)", got, want)
	}
}

func TestIntegration_RefundRepo_SumIsZeroWithoutRefunds(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	inv := seedInvoice(t, db, merchantID, 1000, constant.InvoicePaid, time.Now().Add(time.Hour))

	got, err := NewRefundRepo(db).SumOutstandingByInvoice(context.Background(), inv.ID)
	if err != nil {
		t.Fatalf("SumOutstandingByInvoice: %v", err)
	}
	if got != 0 {
		t.Errorf("outstanding = %d, want 0", got)
	}
}

// ── lookups, filters and pagination (SRS §3.2) ────────────────────────────────

func TestIntegration_InvoiceRepo_FindByPaymentTokenAndNotFound(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewInvoiceRepo(db)
	ctx := context.Background()

	inv := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, time.Now().Add(time.Hour))

	found, err := repo.FindByPaymentToken(ctx, inv.PaymentToken)
	if err != nil {
		t.Fatalf("FindByPaymentToken: %v", err)
	}
	if found.ID != inv.ID {
		t.Errorf("found %s, want %s", found.ID, inv.ID)
	}

	if _, err := repo.FindByPaymentToken(ctx, "no-such-token"); !errors.Is(err, apperror.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := repo.FindByID(ctx, uuid.New()); !errors.Is(err, apperror.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestIntegration_InvoiceRepo_ListFiltersAndPaginates(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	other := seedMerchant(t, db, 0)
	repo := NewInvoiceRepo(db)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	seedInvoice(t, db, merchantID, 100, constant.InvoicePending, future)
	seedInvoice(t, db, merchantID, 200, constant.InvoicePaid, future)
	seedInvoice(t, db, merchantID, 300, constant.InvoicePaid, future)
	seedInvoice(t, db, other, 400, constant.InvoicePaid, future)

	// Scoped to the merchant.
	_, total, err := repo.List(ctx, InvoiceFilter{MerchantID: &merchantID}, 0, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	// Plus a status filter.
	paid := constant.InvoicePaid
	items, total, err := repo.List(ctx, InvoiceFilter{MerchantID: &merchantID, Status: &paid}, 0, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Errorf("total = %d (len %d), want 2 PAID invoices", total, len(items))
	}

	// Pagination: total stays the full count while the page is limited.
	items, total, err = repo.List(ctx, InvoiceFilter{MerchantID: &merchantID}, 0, 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || total != 3 {
		t.Errorf("page: len = %d, total = %d; want 2 and 3", len(items), total)
	}

	// A date window that excludes everything.
	longAgo := time.Now().Add(-48 * time.Hour)
	_, total, err = repo.List(ctx, InvoiceFilter{MerchantID: &merchantID, To: &longAgo}, 0, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 for a window in the past", total)
	}
}

func TestIntegration_PaymentIntentRepo_ListFilterAndLatestSuccess(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewPaymentIntentRepo(db)
	ctx := context.Background()

	inv := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, time.Now().Add(time.Hour))

	mk := func(status constant.PaymentIntentStatus, processedAt *time.Time) *model.PaymentIntent {
		p := &model.PaymentIntent{
			ID: uuid.New(), InvoiceID: inv.ID, Method: constant.PaymentMethodVADummy,
			Status: status, Amount: 1000, ProcessedAt: processedAt,
		}
		if err := repo.Create(ctx, p); err != nil {
			t.Fatalf("creating intent: %v", err)
		}
		return p
	}

	earlier := time.Now().Add(-time.Hour).UTC()
	later := time.Now().UTC()
	mk(constant.PaymentFailed, &earlier)
	mk(constant.PaymentSuccess, &earlier)
	newest := mk(constant.PaymentSuccess, &later)
	mk(constant.PaymentPending, nil)

	// Filter by invoice.
	_, total, err := repo.List(ctx, PaymentIntentFilter{InvoiceID: &inv.ID}, 0, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}

	// Filter by status.
	success := constant.PaymentSuccess
	_, total, err = repo.List(ctx, PaymentIntentFilter{InvoiceID: &inv.ID, Status: &success}, 0, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Errorf("SUCCESS total = %d, want 2", total)
	}

	// The most recently processed SUCCESS wins.
	latest, err := repo.FindLatestSuccessByInvoice(ctx, inv.ID)
	if err != nil {
		t.Fatalf("FindLatestSuccessByInvoice: %v", err)
	}
	if latest.ID != newest.ID {
		t.Errorf("latest success = %s, want %s", latest.ID, newest.ID)
	}

	// An invoice with no successful payment reports NOT_FOUND.
	bare := seedInvoice(t, db, merchantID, 500, constant.InvoicePending, time.Now().Add(time.Hour))
	if _, err := repo.FindLatestSuccessByInvoice(ctx, bare.ID); !errors.Is(err, apperror.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// FindByIDForUpdate is what serializes concurrent refund requests. Two transactions
// must not hold the lock at once.
func TestIntegration_InvoiceRepo_FindByIDForUpdateSerializes(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewInvoiceRepo(db)
	uow := transaction.NewUoW(db)
	ctx := context.Background()

	inv := seedInvoice(t, db, merchantID, 1000, constant.InvoicePaid, time.Now().Add(time.Hour))

	firstHoldsLock := make(chan struct{})
	secondAcquired := make(chan struct{})
	release := make(chan struct{})

	go func() {
		_ = uow.Do(ctx, func(txCtx context.Context) error {
			if _, err := repo.FindByIDForUpdate(txCtx, inv.ID); err != nil {
				return err
			}
			close(firstHoldsLock)
			<-release // hold the lock
			return nil
		})
	}()

	<-firstHoldsLock

	go func() {
		_ = uow.Do(ctx, func(txCtx context.Context) error {
			if _, err := repo.FindByIDForUpdate(txCtx, inv.ID); err != nil {
				return err
			}
			close(secondAcquired)
			return nil
		})
	}()

	select {
	case <-secondAcquired:
		close(release)
		t.Fatal("the second transaction acquired the row lock while the first still held it")
	case <-time.After(300 * time.Millisecond):
		// Blocked as expected.
	}

	close(release)
	select {
	case <-secondAcquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the second transaction never acquired the lock after release")
	}
}

// ── refresh tokens ────────────────────────────────────────────────────────────

func TestIntegration_RefreshTokenRepo_RevokeIsSingleUse(t *testing.T) {
	db := openTestDB(t)
	userID := seedMerchant(t, db, 0)
	repo := NewRefreshTokenRepo(db)
	ctx := context.Background()

	tok := &model.RefreshToken{
		ID: uuid.New(), UserID: userID, TokenHash: uuid.NewString(),
		ExpiresAt: time.Now().Add(24 * time.Hour).UTC(),
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}

	found, err := repo.FindByHash(ctx, tok.TokenHash)
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if !found.IsActive(time.Now().UTC()) {
		t.Error("a freshly created token should be active")
	}

	now := time.Now().UTC()
	if err := repo.Revoke(ctx, tok.ID, now); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Revoking twice must not succeed — that is what makes rotation single-use.
	if err := repo.Revoke(ctx, tok.ID, now); !errors.Is(err, apperror.ErrInvalidState) {
		t.Errorf("second Revoke: want ErrInvalidState, got %v", err)
	}

	found, err = repo.FindByHash(ctx, tok.TokenHash)
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if found.IsActive(time.Now().UTC()) {
		t.Error("a revoked token must not be active")
	}
}

func TestIntegration_RefreshTokenRepo_RevokeAllForUser(t *testing.T) {
	db := openTestDB(t)
	userID := seedMerchant(t, db, 0)
	repo := NewRefreshTokenRepo(db)
	ctx := context.Background()

	hashes := make([]string, 3)
	for i := range hashes {
		hashes[i] = uuid.NewString()
		if err := repo.Create(ctx, &model.RefreshToken{
			ID: uuid.New(), UserID: userID, TokenHash: hashes[i],
			ExpiresAt: time.Now().Add(24 * time.Hour).UTC(),
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	if err := repo.RevokeAllForUser(ctx, userID, time.Now().UTC()); err != nil {
		t.Fatalf("RevokeAllForUser: %v", err)
	}
	for _, h := range hashes {
		found, err := repo.FindByHash(ctx, h)
		if err != nil {
			t.Fatalf("FindByHash: %v", err)
		}
		if found.IsActive(time.Now().UTC()) {
			t.Errorf("token %s is still active after RevokeAllForUser", h)
		}
	}

	// Revoking again is a no-op, not an error.
	if err := repo.RevokeAllForUser(ctx, userID, time.Now().UTC()); err != nil {
		t.Errorf("RevokeAllForUser must be idempotent: %v", err)
	}
}

// ── dashboard aggregation (SRS §2.6) ──────────────────────────────────────────

func TestIntegration_DashboardRepo_Stats(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	other := seedMerchant(t, db, 0)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	paidA := seedInvoice(t, db, merchantID, 10000, constant.InvoicePaid, future)
	seedInvoice(t, db, merchantID, 5000, constant.InvoicePaid, future)
	seedInvoice(t, db, merchantID, 3000, constant.InvoiceExpired, future)
	seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, future)
	// Another merchant's invoice must not leak into the filtered view.
	seedInvoice(t, db, other, 99999, constant.InvoicePaid, future)

	piRepo := NewPaymentIntentRepo(db)
	failed := &model.PaymentIntent{
		ID: uuid.New(), InvoiceID: paidA.ID, Method: constant.PaymentMethodVADummy,
		Status: constant.PaymentFailed, Amount: 10000,
	}
	if err := piRepo.Create(ctx, failed); err != nil {
		t.Fatalf("creating intent: %v", err)
	}
	succeeded := &model.PaymentIntent{
		ID: uuid.New(), InvoiceID: paidA.ID, Method: constant.PaymentMethodVADummy,
		Status: constant.PaymentSuccess, Amount: 10000,
	}
	if err := piRepo.Create(ctx, succeeded); err != nil {
		t.Fatalf("creating intent: %v", err)
	}

	refundRepo := NewRefundRepo(db)
	for _, r := range []struct {
		amount int64
		status constant.RefundStatus
	}{
		{2000, constant.RefundSuccess},
		{500, constant.RefundRequested}, // not settled → excluded from the amount
	} {
		if err := refundRepo.Create(ctx, &model.Refund{
			ID: uuid.New(), InvoiceID: paidA.ID, PaymentIntentID: succeeded.ID,
			MerchantID: merchantID, Amount: r.amount, Status: r.status,
		}); err != nil {
			t.Fatalf("creating refund: %v", err)
		}
	}

	stats, err := NewDashboardRepo(db).Stats(ctx, DashboardFilter{MerchantID: &merchantID})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	if stats.TotalInvoices != 4 {
		t.Errorf("total_invoices = %d, want 4", stats.TotalInvoices)
	}
	if stats.TotalPaid != 2 {
		t.Errorf("total_paid = %d, want 2", stats.TotalPaid)
	}
	if stats.TotalExpired != 1 {
		t.Errorf("total_expired = %d, want 1", stats.TotalExpired)
	}
	if stats.TotalFailed != 1 {
		t.Errorf("total_failed = %d, want 1", stats.TotalFailed)
	}
	if stats.TotalAmountPaid != 15000 {
		t.Errorf("total_amount_paid = %d, want 15000", stats.TotalAmountPaid)
	}
	if stats.TotalAmountRefund != 2000 {
		t.Errorf("total_amount_refund = %d, want 2000 (SUCCESS only)", stats.TotalAmountRefund)
	}
}

func TestIntegration_DashboardRepo_DateWindowExcludesEverything(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	seedInvoice(t, db, merchantID, 1000, constant.InvoicePaid, time.Now().Add(time.Hour))

	to := time.Now().Add(-48 * time.Hour)
	stats, err := NewDashboardRepo(db).Stats(context.Background(),
		DashboardFilter{MerchantID: &merchantID, To: &to})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalInvoices != 0 || stats.TotalAmountPaid != 0 {
		t.Errorf("a window in the past should be empty: %+v", stats)
	}
}

// ── advisory lock (sweeper election) ──────────────────────────────────────────

// pg_try_advisory_xact_lock must be non-blocking: the second holder gets false rather
// than waiting. That is what lets a losing replica skip its tick instead of piling up.
func TestIntegration_AdvisoryLock_SecondCallerIsRefused(t *testing.T) {
	db := openTestDB(t)
	locker := NewAdvisoryLocker(db)
	uow := transaction.NewUoW(db)
	ctx := context.Background()

	firstHolds := make(chan struct{})
	secondVerdict := make(chan bool, 1)
	release := make(chan struct{})

	go func() {
		_ = uow.Do(ctx, func(txCtx context.Context) error {
			ok, err := locker.TryLockTx(txCtx, LockInvoiceExpirySweep)
			if err != nil {
				return err
			}
			if !ok {
				t.Error("the first caller should have acquired the lock")
			}
			close(firstHolds)
			<-release // hold it open
			return nil
		})
	}()

	<-firstHolds

	// A second transaction must be refused immediately, not blocked.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = uow.Do(ctx, func(txCtx context.Context) error {
			ok, err := locker.TryLockTx(txCtx, LockInvoiceExpirySweep)
			if err != nil {
				return err
			}
			secondVerdict <- ok
			return nil
		})
	}()

	select {
	case ok := <-secondVerdict:
		if ok {
			t.Error("two transactions held the same advisory lock at once")
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("TryLockTx blocked; it must return false immediately when contended")
	}

	close(release)
	<-done
}

// The lock is transaction-scoped, so COMMIT releases it. With a connection pool a
// session-scoped lock could be unlocked on the wrong connection and leak forever, which
// is exactly why pg_try_advisory_xact_lock is used.
func TestIntegration_AdvisoryLock_ReleasedOnCommit(t *testing.T) {
	db := openTestDB(t)
	locker := NewAdvisoryLocker(db)
	uow := transaction.NewUoW(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		err := uow.Do(ctx, func(txCtx context.Context) error {
			ok, err := locker.TryLockTx(txCtx, LockInvoiceExpirySweep)
			if err != nil {
				return err
			}
			if !ok {
				t.Errorf("iteration %d could not acquire a lock that should be free", i)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

// Rollback must release it too, or one failed sweep would wedge the sweeper permanently.
func TestIntegration_AdvisoryLock_ReleasedOnRollback(t *testing.T) {
	db := openTestDB(t)
	locker := NewAdvisoryLocker(db)
	uow := transaction.NewUoW(db)
	ctx := context.Background()
	sentinel := errors.New("deliberate failure")

	err := uow.Do(ctx, func(txCtx context.Context) error {
		if _, err := locker.TryLockTx(txCtx, LockInvoiceExpirySweep); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want the sentinel error, got %v", err)
	}

	// Must be free again.
	if err := uow.Do(ctx, func(txCtx context.Context) error {
		ok, err := locker.TryLockTx(txCtx, LockInvoiceExpirySweep)
		if err != nil {
			return err
		}
		if !ok {
			t.Error("the lock was not released by ROLLBACK")
		}
		return nil
	}); err != nil {
		t.Fatalf("re-acquiring: %v", err)
	}
}

// Different keys are independent locks.
func TestIntegration_AdvisoryLock_KeysAreIndependent(t *testing.T) {
	db := openTestDB(t)
	locker := NewAdvisoryLocker(db)
	uow := transaction.NewUoW(db)
	ctx := context.Background()

	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = uow.Do(ctx, func(txCtx context.Context) error {
			if _, err := locker.TryLockTx(txCtx, LockInvoiceExpirySweep); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	err := uow.Do(ctx, func(txCtx context.Context) error {
		ok, err := locker.TryLockTx(txCtx, AdvisoryLockKey(int64(LockInvoiceExpirySweep)+1))
		if err != nil {
			return err
		}
		if !ok {
			t.Error("a different lock key must be independently acquirable")
		}
		return nil
	})
	close(release)
	if err != nil {
		t.Fatalf("second key: %v", err)
	}
}

// ── intent settlement on expiry ───────────────────────────────────────────────

func TestIntegration_PaymentIntentRepo_FailPendingForExpiredInvoices(t *testing.T) {
	db := openTestDB(t)
	merchantID := seedMerchant(t, db, 0)
	repo := NewPaymentIntentRepo(db)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	expired := seedInvoice(t, db, merchantID, 1000, constant.InvoiceExpired, future)
	pending := seedInvoice(t, db, merchantID, 1000, constant.InvoicePending, future)
	paid := seedInvoice(t, db, merchantID, 1000, constant.InvoicePaid, future)

	mk := func(invoiceID uuid.UUID, status constant.PaymentIntentStatus) uuid.UUID {
		p := &model.PaymentIntent{
			ID: uuid.New(), InvoiceID: invoiceID, Method: constant.PaymentMethodVADummy,
			Status: status, Amount: 1000,
		}
		if err := repo.Create(ctx, p); err != nil {
			t.Fatalf("creating intent: %v", err)
		}
		return p.ID
	}

	target := mk(expired.ID, constant.PaymentPending)  // must be failed
	settled := mk(expired.ID, constant.PaymentSuccess) // terminal, untouched
	live := mk(pending.ID, constant.PaymentPending)    // invoice still open
	onPaid := mk(paid.ID, constant.PaymentPending)     // invoice paid

	now := time.Now().UTC()
	n, err := repo.FailPendingForExpiredInvoices(ctx, now)
	if err != nil {
		t.Fatalf("FailPendingForExpiredInvoices: %v", err)
	}
	if n != 1 {
		t.Errorf("affected %d rows, want exactly 1", n)
	}

	for name, tc := range map[string]struct {
		id   uuid.UUID
		want constant.PaymentIntentStatus
	}{
		"pending intent of an EXPIRED invoice": {target, constant.PaymentFailed},
		"already SUCCESS intent":               {settled, constant.PaymentSuccess},
		"pending intent of a PENDING invoice":  {live, constant.PaymentPending},
		"pending intent of a PAID invoice":     {onPaid, constant.PaymentPending},
	} {
		got, err := repo.FindByID(ctx, tc.id)
		if err != nil {
			t.Fatalf("FindByID(%s): %v", name, err)
		}
		if got.Status != tc.want {
			t.Errorf("%s: status = %s, want %s", name, got.Status, tc.want)
		}
	}

	// processed_at must be recorded on the one that changed.
	got, err := repo.FindByID(ctx, target)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ProcessedAt == nil {
		t.Error("processed_at must be set on the settled intent")
	}

	// Idempotent: a second sweep finds nothing left.
	if n, err := repo.FailPendingForExpiredInvoices(ctx, now); err != nil || n != 0 {
		t.Errorf("second sweep: n=%d err=%v, want 0 and no error", n, err)
	}
}

// ── migrations ────────────────────────────────────────────────────────────────

// Migrate records applied versions, so re-running it must be a no-op.
func TestIntegration_Migrate_IsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	var before int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&before); err != nil {
		t.Fatalf("counting migrations: %v", err)
	}
	if before == 0 {
		t.Fatal("no migrations were recorded")
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("re-running Migrate: %v", err)
	}

	var after int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatalf("counting migrations: %v", err)
	}
	if after != before {
		t.Errorf("migration count changed from %d to %d on a re-run", before, after)
	}
}
