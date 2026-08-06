//go:build e2e

package e2e

import (
	"net/http"
	"sync"
	"testing"
)

// TestE2E_Refund_HappyPath walks the two-stage graph from SRS section 2.5:
// REQUESTED → APPROVED → SUCCESS, with the wallet debited only on the last step.
//
// Checking the balance after *each* transition is the point. Debiting on APPROVE instead
// of on SUCCESS would still end with the right final number, and a test that only looked
// at the end would miss it.
func TestE2E_Refund_HappyPath(t *testing.T) {
	m := newMerchant(t)
	inv := paidInvoice(t, m, 100_000)

	rf := m.mustRequestRefund(t, inv.ID, 40_000)
	if got := m.balance(t); got != 100_000 {
		t.Fatalf("balance = %d after merely requesting a refund, want 100000 — nothing moves until SUCCESS", got)
	}

	if got := mustActOnRefund(t, rf.ID, "APPROVE").Status; got != "APPROVED" {
		t.Fatalf("refund status = %q after APPROVE", got)
	}
	if got := m.balance(t); got != 100_000 {
		t.Fatalf("balance = %d after APPROVE, want 100000 — approval authorises, it does not pay", got)
	}

	if got := mustActOnRefund(t, rf.ID, "PROCESS").Status; got != "SUCCESS" {
		t.Fatalf("refund status = %q after PROCESS", got)
	}
	if got := m.balance(t); got != 60_000 {
		t.Errorf("balance = %d after the payout, want 60000 (100000 - 40000)", got)
	}
}

// TestE2E_Refund_ApprovalCannotBeSkipped is the invariant that stops a merchant paying
// itself: there is no REQUESTED → SUCCESS edge, because SUCCESS is what moves the money
// and only an admin may authorise it (agent_documentation/03-state-machines.md section 6).
func TestE2E_Refund_ApprovalCannotBeSkipped(t *testing.T) {
	m := newMerchant(t)
	inv := paidInvoice(t, m, 50_000)
	rf := m.mustRequestRefund(t, inv.ID, 50_000)

	requireErrorCode(t, actOnRefund(t, rf.ID, "PROCESS"), http.StatusUnprocessableEntity, "INVALID_STATE")

	if got := m.balance(t); got != 50_000 {
		t.Errorf("balance = %d after a refused payout, want 50000 — money moved on a rejected transition", got)
	}
}

// TestE2E_Refund_CumulativeCapIsEnforced is the regression test for the over-refund bug
// documented in agent_documentation/02-data-integrity.md section 5.
//
// The original code compared each request against the invoice total in isolation, so an
// invoice of 100000 accepted any number of 100000 refunds. The cap has to be cumulative:
// everything already claimed plus this request must fit inside the invoice.
func TestE2E_Refund_CumulativeCapIsEnforced(t *testing.T) {
	m := newMerchant(t)
	inv := paidInvoice(t, m, 100_000)

	// Two partial refunds that together use up the invoice.
	first := m.mustRequestRefund(t, inv.ID, 60_000)
	mustActOnRefund(t, first.ID, "APPROVE")
	mustActOnRefund(t, first.ID, "PROCESS")

	second := m.mustRequestRefund(t, inv.ID, 40_000)
	mustActOnRefund(t, second.ID, "APPROVE")
	mustActOnRefund(t, second.ID, "PROCESS")

	if got := m.balance(t); got != 0 {
		t.Fatalf("balance = %d after refunding the invoice in full, want 0", got)
	}

	// Nothing is left to refund, however small the request.
	requireErrorCode(t, m.requestRefund(t, inv.ID, 1),
		http.StatusUnprocessableEntity, "REFUND_EXCEEDS_INVOICE")

	if got := m.balance(t); got != 0 {
		t.Errorf("balance = %d, want 0 — the refused request still moved money", got)
	}
}

// TestE2E_Refund_PendingClaimsAreReserved is the subtler half of the cap. If only SUCCESS
// counted, a merchant could file many full-value requests while each is still REQUESTED
// and have an admin approve them all — the same bug, moved one step later. Amounts are
// therefore reserved from the moment they are asked for.
func TestE2E_Refund_PendingClaimsAreReserved(t *testing.T) {
	m := newMerchant(t)
	inv := paidInvoice(t, m, 100_000)

	// Left REQUESTED on purpose: not approved, not paid, but holding its claim.
	m.mustRequestRefund(t, inv.ID, 70_000)

	requireErrorCode(t, m.requestRefund(t, inv.ID, 40_000),
		http.StatusUnprocessableEntity, "REFUND_EXCEEDS_INVOICE")

	// What does fit is still allowed, so the reservation is exact rather than a blanket lock.
	requireStatus(t, m.requestRefund(t, inv.ID, 30_000), http.StatusCreated)
}

// TestE2E_Refund_RejectedAndFailedReleaseTheirClaim is the flip side: a claim that ended
// in failure must free its amount, otherwise one mistaken request would permanently strand
// part of an invoice.
func TestE2E_Refund_RejectedAndFailedReleaseTheirClaim(t *testing.T) {
	m := newMerchant(t)
	inv := paidInvoice(t, m, 80_000)

	rejected := m.mustRequestRefund(t, inv.ID, 80_000)
	if got := mustActOnRefund(t, rejected.ID, "REJECT").Status; got != "REJECTED" {
		t.Fatalf("refund status = %q after REJECT", got)
	}

	// The full amount is available again.
	failed := m.mustRequestRefund(t, inv.ID, 80_000)
	mustActOnRefund(t, failed.ID, "APPROVE")
	if got := mustActOnRefund(t, failed.ID, "FAIL").Status; got != "FAILED" {
		t.Fatalf("refund status = %q after FAIL", got)
	}
	if got := m.balance(t); got != 80_000 {
		t.Fatalf("balance = %d after a FAILED payout, want 80000 — a failed refund must not debit", got)
	}

	// And once more after the failure, which now succeeds.
	final := m.mustRequestRefund(t, inv.ID, 80_000)
	mustActOnRefund(t, final.ID, "APPROVE")
	mustActOnRefund(t, final.ID, "PROCESS")

	if got := m.balance(t); got != 0 {
		t.Errorf("balance = %d after the successful retry, want 0", got)
	}
}

// TestE2E_Refund_ConcurrentRequestsCannotExceedInvoice is the write-skew scenario from
// agent_documentation/02-data-integrity.md section 5, driven through HTTP.
//
// Two requests of 60000 against a 100000 invoice each read an outstanding total of 0 and
// each conclude they fit. Neither a transaction nor CAS prevents this — the rows are being
// INSERTed, so there is no prior status to compare against, and READ COMMITTED lets both
// SUMs see the same thing. Only the `SELECT ... FOR UPDATE` on the invoice row serialises
// them. That the accepted total never exceeds the invoice is the observable proof.
func TestE2E_Refund_ConcurrentRequestsCannotExceedInvoice(t *testing.T) {
	m := newMerchant(t)
	inv := paidInvoice(t, m, 100_000)

	const (
		racers = 4
		each   = int64(60_000) // any two of these would overshoot
	)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
		refused  int
		start    = make(chan struct{})
	)

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp := m.requestRefund(t, inv.ID, each)

			mu.Lock()
			defer mu.Unlock()
			switch resp.Status {
			case http.StatusCreated:
				accepted++
			case http.StatusUnprocessableEntity:
				refused++
			default:
				t.Errorf("unexpected status %d from a concurrent refund request\nbody: %s", resp.Status, resp.Body)
			}
		}()
	}
	close(start)
	wg.Wait()

	// 100000 / 60000 = 1. A second acceptance means the invariant was violated.
	if accepted != 1 {
		t.Errorf("%d concurrent refund requests of %d were accepted against a 100000 invoice, want exactly 1",
			accepted, each)
	}
	if refused != racers-1 {
		t.Errorf("%d requests were refused, want %d", refused, racers-1)
	}
}

// TestE2E_Refund_UnpaidInvoiceCannotBeRefunded stops money leaving for an invoice that
// never brought any in.
func TestE2E_Refund_UnpaidInvoiceCannotBeRefunded(t *testing.T) {
	m := fundedMerchant(t, 100_000) // has funds, so a debit would actually succeed
	unpaid := m.createInvoice(t, 50_000)

	requireErrorCode(t, m.requestRefund(t, unpaid.ID, 50_000),
		http.StatusUnprocessableEntity, "INVOICE_NOT_PAID")

	if got := m.balance(t); got != 100_000 {
		t.Errorf("balance = %d, want 100000", got)
	}
}

// TestE2E_Refund_MerchantCannotRefundForeignInvoice is the ownership check. Without it,
// knowing an invoice id would be enough to pull money out of someone else's wallet —
// broken object-level authorisation, in the most expensive place it could occur.
func TestE2E_Refund_MerchantCannotRefundForeignInvoice(t *testing.T) {
	victim := newMerchant(t)
	attacker := newMerchant(t)

	inv := paidInvoice(t, victim, 70_000)

	resp := attacker.requestRefund(t, inv.ID, 70_000)
	if resp.Status == http.StatusCreated {
		t.Fatal("a merchant filed a refund against another merchant's invoice")
	}
	if resp.Status != http.StatusNotFound && resp.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 404 or 403\nbody: %s", resp.Status, resp.Body)
	}

	if got := victim.balance(t); got != 70_000 {
		t.Errorf("victim balance = %d, want 70000", got)
	}
}

// TestE2E_Refund_TerminalStatesAcceptNoFurtherAction confirms the graph has no way out of
// a terminal state, tried from every direction rather than sampled.
func TestE2E_Refund_TerminalStatesAcceptNoFurtherAction(t *testing.T) {
	m := newMerchant(t)
	inv := paidInvoice(t, m, 90_000)

	rf := m.mustRequestRefund(t, inv.ID, 90_000)
	mustActOnRefund(t, rf.ID, "APPROVE")
	mustActOnRefund(t, rf.ID, "PROCESS") // now SUCCESS, terminal

	for _, action := range []string{"APPROVE", "REJECT", "PROCESS", "FAIL"} {
		t.Run("after_success_"+action, func(t *testing.T) {
			requireErrorCode(t, actOnRefund(t, rf.ID, action),
				http.StatusUnprocessableEntity, "INVALID_STATE")
		})
	}

	if got := m.balance(t); got != 0 {
		t.Errorf("balance = %d after refusing four extra actions, want 0 — one of them paid out again", got)
	}
}

// TestE2E_Refund_MerchantSeesOnlyItsOwnRefunds checks the list endpoint scopes by the
// caller's identity from the token, not by a client-supplied filter.
func TestE2E_Refund_MerchantSeesOnlyItsOwnRefunds(t *testing.T) {
	mine := newMerchant(t)
	theirs := newMerchant(t)

	myInvoice := paidInvoice(t, mine, 20_000)
	myRefund := mine.mustRequestRefund(t, myInvoice.ID, 20_000)

	theirInvoice := paidInvoice(t, theirs, 20_000)
	theirRefund := theirs.mustRequestRefund(t, theirInvoice.ID, 20_000)

	r := mine.get(t, "/api/v1/refunds?page=1&page_size=100")
	requireStatus(t, r, http.StatusOK)
	page := decode[paginated[refundResponse]](t, r)

	var sawMine bool
	for _, rf := range page.Data {
		if rf.ID == theirRefund.ID {
			t.Error("a merchant's refund list contained another merchant's refund")
		}
		if rf.ID == myRefund.ID {
			sawMine = true
		}
	}
	if !sawMine {
		t.Error("a merchant's refund list did not contain its own refund")
	}
}
