//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestE2E_Payment_HappyPath is the reference journey from SRS sections 2.3–2.4: merchant
// issues an invoice, an anonymous payer settles it through the public link, an admin marks
// it SUCCESS, and the money lands.
//
// The balance assertion is absolute rather than relative, which is what makes it a
// double-credit check as well as a credit check.
func TestE2E_Payment_HappyPath(t *testing.T) {
	m := newMerchant(t)

	if got := m.balance(t); got != 0 {
		t.Fatalf("a new merchant starts with balance %d, want 0", got)
	}

	inv := m.createInvoice(t, 50_000)

	// The payment link must be usable by someone with no account at all.
	public := newClient()
	r := public.get(t, "/api/v1/pay/"+inv.PaymentToken)
	requireStatus(t, r, http.StatusOK)
	if pub := decode[publicInvoiceResponse](t, r); pub.Amount != 50_000 || pub.Status != "PENDING" {
		t.Fatalf("public invoice = %+v, want amount 50000 and status PENDING", pub)
	}

	intent := createIntent(t, public, inv.PaymentToken, "VA_DUMMY")
	if intent.Amount != 50_000 {
		t.Fatalf("intent amount = %d, want the invoice amount 50000", intent.Amount)
	}

	// The payer can poll their own intent through the token-scoped route (SRS 4.3).
	r = public.get(t, "/api/v1/pay/"+inv.PaymentToken+"/intents/"+intent.ID)
	requireStatus(t, r, http.StatusOK)
	if got := decode[intentResponse](t, r).Status; got != "PENDING" {
		t.Fatalf("polled intent status = %q before settlement, want PENDING", got)
	}

	requireStatus(t, settleIntent(t, intent.ID, "SUCCESS"), http.StatusOK)

	if got := publicStatus(t, inv.PaymentToken); got != "PAID" {
		t.Errorf("invoice status = %q after SUCCESS, want PAID", got)
	}
	if got := m.balance(t); got != 50_000 {
		t.Errorf("merchant balance = %d, want exactly 50000", got)
	}

	r = public.get(t, "/api/v1/pay/"+inv.PaymentToken+"/intents/"+intent.ID)
	requireStatus(t, r, http.StatusOK)
	if got := decode[intentResponse](t, r).Status; got != "SUCCESS" {
		t.Errorf("polled intent status = %q after settlement, want SUCCESS", got)
	}
}

// TestE2E_Payment_SecondSettleIsRejected is the sequential half of the double-processing
// guard: SUCCESS is terminal, so settling twice must fail — and must not credit twice.
//
// The balance check is the part that matters. A 422 with the money already moved is still
// a financial bug.
func TestE2E_Payment_SecondSettleIsRejected(t *testing.T) {
	m := newMerchant(t)
	inv := m.createInvoice(t, 30_000)
	intent := createIntent(t, newClient(), inv.PaymentToken, "VA_DUMMY")

	requireStatus(t, settleIntent(t, intent.ID, "SUCCESS"), http.StatusOK)
	requireErrorCode(t, settleIntent(t, intent.ID, "SUCCESS"), http.StatusUnprocessableEntity, "INVALID_STATE")

	if got := m.balance(t); got != 30_000 {
		t.Errorf("balance = %d after a rejected second settlement, want 30000 — it was credited twice", got)
	}
}

// TestE2E_Payment_ConcurrentSettlementsCreditOnce is the race the sequential test cannot
// reach: two admins pressing SUCCESS at the same moment.
//
// Both requests read PENDING and both clear the service-level FSM check, because that
// check ran before either wrote. Only the CAS in SQL (`WHERE id = ? AND status = ?`) can
// separate them — see agent_documentation/02-data-integrity.md section 4. Exactly one 200
// and a single credit is the proof it is doing that through the full stack.
func TestE2E_Payment_ConcurrentSettlementsCreditOnce(t *testing.T) {
	m := newMerchant(t)
	inv := m.createInvoice(t, 40_000)
	intent := createIntent(t, newClient(), inv.PaymentToken, "VA_DUMMY")

	const racers = 4
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []int
		start   = make(chan struct{})
	)

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release together, so the reads genuinely overlap
			resp := settleIntent(t, intent.ID, "SUCCESS")
			mu.Lock()
			results = append(results, resp.Status)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	var ok, rejected int
	for _, status := range results {
		switch status {
		case http.StatusOK:
			ok++
		case http.StatusUnprocessableEntity:
			rejected++
		default:
			t.Errorf("unexpected status %d from a concurrent settlement", status)
		}
	}

	if ok != 1 {
		t.Errorf("%d of %d concurrent settlements succeeded, want exactly 1", ok, racers)
	}
	if rejected != racers-1 {
		t.Errorf("%d settlements were rejected, want %d", rejected, racers-1)
	}
	if got := m.balance(t); got != 40_000 {
		t.Errorf("balance = %d after %d concurrent settlements, want 40000", got, racers)
	}
}

// TestE2E_Payment_FailedIntentLeavesInvoicePayable checks the other branch of the intent
// FSM. FAILED is terminal for that intent, but it must not close the invoice: the payer
// is expected to retry with a new one, which is what gives each attempt its own audit
// trail (agent_documentation/03-state-machines.md section 6).
func TestE2E_Payment_FailedIntentLeavesInvoicePayable(t *testing.T) {
	m := newMerchant(t)
	inv := m.createInvoice(t, 25_000)

	first := createIntent(t, newClient(), inv.PaymentToken, "VA_DUMMY")
	requireStatus(t, settleIntent(t, first.ID, "FAILED"), http.StatusOK)

	if got := publicStatus(t, inv.PaymentToken); got != "PENDING" {
		t.Fatalf("invoice status = %q after a FAILED attempt, want PENDING — a failed attempt must not close the invoice", got)
	}
	if got := m.balance(t); got != 0 {
		t.Fatalf("balance = %d after a FAILED payment, want 0", got)
	}

	// A failed intent is terminal: it cannot be revived into a success.
	requireErrorCode(t, settleIntent(t, first.ID, "SUCCESS"), http.StatusUnprocessableEntity, "INVALID_STATE")

	// Retrying means a brand new intent, and that one settles normally.
	second := createIntent(t, newClient(), inv.PaymentToken, "VA_DUMMY")
	if second.ID == first.ID {
		t.Fatal("retrying reused the failed intent; each attempt must be its own record")
	}
	requireStatus(t, settleIntent(t, second.ID, "SUCCESS"), http.StatusOK)

	if got := m.balance(t); got != 25_000 {
		t.Errorf("balance = %d after the retry succeeded, want 25000", got)
	}
}

// TestE2E_Payment_WalletMethodMovesMoneyBothWays covers the one method with two sides.
// WALLET credits the merchant and debits the payer when the payer is authenticated, and
// the strategy runs inside the same transaction as the status change — so either both
// wallets move or neither does (agent_documentation/01-architecture.md section 4).
func TestE2E_Payment_WalletMethodMovesMoneyBothWays(t *testing.T) {
	seller := newMerchant(t)
	payer := fundedMerchant(t, 100_000)

	inv := seller.createInvoice(t, 60_000)

	// The payer is logged in, so the intent records who is paying.
	intent := createIntent(t, payer.client, inv.PaymentToken, "WALLET")
	requireStatus(t, settleIntent(t, intent.ID, "SUCCESS"), http.StatusOK)

	if got := seller.balance(t); got != 60_000 {
		t.Errorf("seller balance = %d, want 60000", got)
	}
	if got := payer.balance(t); got != 40_000 {
		t.Errorf("payer balance = %d, want 40000 (100000 - 60000)", got)
	}
}

// TestE2E_Payment_WalletPayerCannotOverdraw pins the non-negative balance guard, which
// lives in SQL as `WHERE balance + $1 >= 0` and therefore holds regardless of what the
// application layer believes (agent_documentation/02-data-integrity.md section 6).
//
// It also pins atomicity in the direction that matters most: when the payer's debit is
// refused, the seller must not have been credited either.
func TestE2E_Payment_WalletPayerCannotOverdraw(t *testing.T) {
	seller := newMerchant(t)
	payer := fundedMerchant(t, 10_000)

	inv := seller.createInvoice(t, 50_000) // more than the payer holds
	intent := createIntent(t, payer.client, inv.PaymentToken, "WALLET")

	resp := settleIntent(t, intent.ID, "SUCCESS")
	if resp.Status == http.StatusOK {
		t.Fatalf("settlement succeeded although the payer could not cover it; seller=%d payer=%d",
			seller.balance(t), payer.balance(t))
	}

	if got := payer.balance(t); got != 10_000 {
		t.Errorf("payer balance = %d, want 10000 — a refused payment must not debit", got)
	}
	if got := seller.balance(t); got != 0 {
		t.Errorf("seller balance = %d, want 0 — the credit must roll back with the failed debit", got)
	}
	if got := publicStatus(t, inv.PaymentToken); got != "PENDING" {
		t.Errorf("invoice status = %q, want PENDING — it must roll back with the transaction", got)
	}
}

// TestE2E_Payment_PastDueInvoiceIsNotPayable covers the double check described in
// agent_documentation/01-architecture.md section 5: CreateIntent rejects a payment past
// due_date even while the sweeper has not yet flipped the status to EXPIRED. Without it
// there is a window, up to one sweep interval wide, where an expired invoice is payable.
//
// The invoice is created with a near due date and then waited out, because the API
// (correctly) refuses to create one that is already overdue.
func TestE2E_Payment_PastDueInvoiceIsNotPayable(t *testing.T) {
	m := newMerchant(t)

	const grace = 3 * time.Second
	inv := m.createInvoiceDue(t, 20_000, time.Now().Add(grace))

	// Payable right now, which confirms the rejection below is about the due date and
	// not about something else being wrong with the invoice.
	early := createIntent(t, newClient(), inv.PaymentToken, "VA_DUMMY")
	requireStatus(t, settleIntent(t, early.ID, "FAILED"), http.StatusOK)

	time.Sleep(grace + time.Second)

	resp := newClient().post(t, "/api/v1/pay/"+inv.PaymentToken, map[string]any{"method": "VA_DUMMY"})
	if resp.Status == http.StatusCreated {
		t.Fatalf("an intent was created for an invoice past its due date: %s", resp.Body)
	}
	if resp.Status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d for a past-due invoice, want 422\nbody: %s", resp.Status, resp.Body)
	}
	if got := m.balance(t); got != 0 {
		t.Errorf("balance = %d, want 0", got)
	}
}

// TestE2E_Payment_UnknownTokenIsNotFound keeps the public surface from confirming which
// tokens exist. The token is the only thing protecting the payment page, so a
// distinguishable response would turn guessing into enumeration.
func TestE2E_Payment_UnknownTokenIsNotFound(t *testing.T) {
	bogus := strings.Repeat("a", 64) // same shape as a real 32-byte hex token
	requireErrorCode(t, newClient().get(t, "/api/v1/pay/"+bogus), http.StatusNotFound, "NOT_FOUND")
}

// TestE2E_Payment_IntentIsScopedToItsToken checks the payer-polling route cannot be used
// to read across invoices. The token in the path must gate the intent in the path;
// otherwise anyone holding one valid link could walk other people's payments.
func TestE2E_Payment_IntentIsScopedToItsToken(t *testing.T) {
	m := newMerchant(t)

	own := m.createInvoice(t, 10_000)
	other := m.createInvoice(t, 11_000)
	otherIntent := createIntent(t, newClient(), other.PaymentToken, "VA_DUMMY")

	// A valid token, a valid intent id — but they belong to different invoices.
	resp := newClient().get(t, "/api/v1/pay/"+own.PaymentToken+"/intents/"+otherIntent.ID)
	requireErrorCode(t, resp, http.StatusNotFound, "NOT_FOUND")
}
