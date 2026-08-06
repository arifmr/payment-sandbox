//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"
)

type dashboardResponse struct {
	TotalInvoices     int64 `json:"total_invoices"`
	TotalPaid         int64 `json:"total_paid"`
	TotalFailed       int64 `json:"total_failed"`
	TotalExpired      int64 `json:"total_expired"`
	TotalAmountPaid   int64 `json:"total_amount_paid"`
	TotalAmountRefund int64 `json:"total_amount_refund"`
}

// TestE2E_Dashboard_AggregatesForASingleMerchant builds a known set of records and reads
// the statistics back filtered to that merchant.
//
// Filtering by merchant is what makes the numbers assertable: the dashboard is global, and
// this database also holds whatever every other scenario created. Filtering turns "the
// number went up" into an exact expectation.
func TestE2E_Dashboard_AggregatesForASingleMerchant(t *testing.T) {
	m := newMerchant(t)

	// Two paid invoices: 100000 + 50000.
	paidInvoice(t, m, 100_000)
	paidInvoice(t, m, 50_000)

	// One invoice with a failed attempt, left unpaid.
	unpaid := m.createInvoice(t, 70_000)
	failed := createIntent(t, newClient(), unpaid.PaymentToken, "VA_DUMMY")
	requireStatus(t, settleIntent(t, failed.ID, "FAILED"), http.StatusOK)

	// One completed refund of 30000.
	refunded := paidInvoice(t, m, 30_000)
	rf := m.mustRequestRefund(t, refunded.ID, 30_000)
	mustActOnRefund(t, rf.ID, "APPROVE")
	mustActOnRefund(t, rf.ID, "PROCESS")

	stats := dashboard(t, "?merchant_id="+m.ID)

	if stats.TotalInvoices != 4 {
		t.Errorf("total_invoices = %d, want 4", stats.TotalInvoices)
	}
	if stats.TotalPaid != 3 {
		t.Errorf("total_paid = %d, want 3", stats.TotalPaid)
	}

	// total_failed counts payment intents, not invoices — invoices have no FAILED state in
	// the SRS state machine. The consequence is documented in
	// agent_documentation/06-trade-offs.md section 2: this figure is not comparable with
	// total_invoices, because one counts attempts and the other counts invoices.
	if stats.TotalFailed != 1 {
		t.Errorf("total_failed = %d, want 1 (failed intents, not invoices)", stats.TotalFailed)
	}

	if stats.TotalAmountRefund != 30_000 {
		t.Errorf("total_amount_refund = %d, want 30000 — only SUCCESS refunds count as money out", stats.TotalAmountRefund)
	}
	if stats.TotalAmountPaid <= 0 {
		t.Errorf("total_amount_paid = %d, want a positive transaction total", stats.TotalAmountPaid)
	}
}

// TestE2E_Dashboard_PendingRefundsAreNotCountedAsMoneyOut separates authorisation from
// payment. A REQUESTED or APPROVED refund reserves part of the invoice, but nothing has
// left the wallet — counting it would overstate refunds and make the dashboard disagree
// with the actual balances.
func TestE2E_Dashboard_PendingRefundsAreNotCountedAsMoneyOut(t *testing.T) {
	m := newMerchant(t)
	inv := paidInvoice(t, m, 80_000)

	requested := m.mustRequestRefund(t, inv.ID, 20_000)
	approved := m.mustRequestRefund(t, inv.ID, 20_000)
	mustActOnRefund(t, approved.ID, "APPROVE")

	if got := dashboard(t, "?merchant_id="+m.ID).TotalAmountRefund; got != 0 {
		t.Errorf("total_amount_refund = %d with nothing paid out yet, want 0", got)
	}
	if got := m.balance(t); got != 80_000 {
		t.Errorf("balance = %d, want 80000 — consistent with total_amount_refund being 0", got)
	}

	// Paying one out moves both numbers together.
	mustActOnRefund(t, requested.ID, "APPROVE")
	mustActOnRefund(t, requested.ID, "PROCESS")

	if got := dashboard(t, "?merchant_id="+m.ID).TotalAmountRefund; got != 20_000 {
		t.Errorf("total_amount_refund = %d after one payout, want 20000", got)
	}
	if got := m.balance(t); got != 60_000 {
		t.Errorf("balance = %d after one payout, want 60000", got)
	}
}

// TestE2E_Dashboard_DateWindowIsApplied checks the from/to filter from SRS section 2.6. A
// window entirely in the past must exclude records created now — if it does not, the filter
// is being ignored and every report silently covers all time.
func TestE2E_Dashboard_DateWindowIsApplied(t *testing.T) {
	m := newMerchant(t)
	paidInvoice(t, m, 45_000)

	if got := dashboard(t, "?merchant_id="+m.ID).TotalInvoices; got != 1 {
		t.Fatalf("total_invoices = %d without a window, want 1", got)
	}

	from := time.Now().AddDate(-1, 0, 0).UTC().Format(time.RFC3339)
	to := time.Now().AddDate(0, -6, 0).UTC().Format(time.RFC3339)

	past := dashboard(t, "?merchant_id="+m.ID+"&from="+from+"&to="+to)
	if past.TotalInvoices != 0 {
		t.Errorf("total_invoices = %d for a window that ended six months ago, want 0", past.TotalInvoices)
	}
	if past.TotalAmountPaid != 0 || past.TotalAmountRefund != 0 {
		t.Errorf("amounts leaked past the date window: %+v", past)
	}
}

// TestE2E_Dashboard_IsAdminOnly keeps merchant statistics from leaking across tenants: the
// endpoint is global by design, so a merchant reaching it would see everyone's figures.
func TestE2E_Dashboard_IsAdminOnly(t *testing.T) {
	m := newMerchant(t)
	requireStatus(t, m.get(t, "/api/v1/admin/dashboard"), http.StatusForbidden)
	requireStatus(t, newClient().get(t, "/api/v1/admin/dashboard"), http.StatusUnauthorized)
}

func dashboard(t *testing.T, query string) dashboardResponse {
	t.Helper()
	r := adminClient().get(t, "/api/v1/admin/dashboard"+query)
	requireStatus(t, r, http.StatusOK)
	return decode[dashboardResponse](t, r)
}
