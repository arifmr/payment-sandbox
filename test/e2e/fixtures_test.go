//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

const testPassword = "password123"

// emailSeq disambiguates accounts created inside the same nanosecond, which happens once
// tests run in parallel. A collision would surface as a confusing 409 EMAIL_TAKEN in an
// unrelated scenario rather than as the conflict test it looks like.
var emailSeq atomic.Int64

func uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s-%d-%d@e2e.test", prefix, time.Now().UnixNano(), emailSeq.Add(1))
}

// merchant is a registered, logged-in merchant plus its authenticated client.
//
// Every scenario builds its own rather than sharing one. Wallet balance is the reason:
// assertions here are absolute ("balance is exactly 50000"), and a shared merchant would
// make them depend on which other tests happened to run first. Absolute assertions catch
// double-credit bugs that relative ones ("balance grew by 50000") let through.
type merchant struct {
	*client
	ID           string
	Email        string
	RefreshToken string
}

func newMerchant(t *testing.T) *merchant {
	t.Helper()

	email := uniqueEmail("merchant")
	anon := newClient()

	r := anon.post(t, "/api/v1/auth/register", map[string]any{
		"name":     "E2E Merchant",
		"email":    email,
		"password": testPassword,
	})
	requireStatus(t, r, http.StatusCreated)
	user := decode[userResponse](t, r)

	if user.Role != "MERCHANT" {
		t.Fatalf("registered role = %q, want MERCHANT — registration must not let the caller pick a role", user.Role)
	}

	sess := loginAs(t, email, testPassword)
	return &merchant{
		client:       anon.as(sess.AccessToken),
		ID:           user.ID,
		Email:        email,
		RefreshToken: sess.RefreshToken,
	}
}

func loginAs(t *testing.T, email, password string) loginResponse {
	t.Helper()
	r := newClient().post(t, "/api/v1/auth/login", map[string]any{"email": email, "password": password})
	requireStatus(t, r, http.StatusOK)
	sess := decode[loginResponse](t, r)
	if sess.AccessToken == "" || sess.RefreshToken == "" {
		t.Fatalf("login returned an incomplete token pair: %s", r.Body)
	}
	return sess
}

// balance reads the merchant's wallet. Used as the assertion of record for anything that
// moves money: status transitions are easy to get right while the balance is wrong, and
// the balance is what a merchant actually cares about.
func (m *merchant) balance(t *testing.T) int64 {
	t.Helper()
	r := m.get(t, "/api/v1/wallet")
	requireStatus(t, r, http.StatusOK)
	return decode[walletResponse](t, r).Balance
}

// createInvoice makes a PENDING invoice due well in the future.
func (m *merchant) createInvoice(t *testing.T, amount int64) invoiceResponse {
	t.Helper()
	return m.createInvoiceDue(t, amount, time.Now().Add(24*time.Hour))
}

func (m *merchant) createInvoiceDue(t *testing.T, amount int64, due time.Time) invoiceResponse {
	t.Helper()
	r := m.post(t, "/api/v1/invoices", map[string]any{
		"customer_name":  "Budi",
		"customer_email": "budi@example.com",
		"description":    "e2e scenario",
		"amount":         amount,
		"due_date":       due.UTC().Format(time.RFC3339Nano),
	})
	requireStatus(t, r, http.StatusCreated)

	inv := decode[invoiceResponse](t, r)
	if inv.Status != "PENDING" {
		t.Fatalf("new invoice status = %q, want PENDING", inv.Status)
	}
	if inv.PaymentToken == "" {
		t.Fatal("new invoice has no payment token; the public payment page would be unreachable")
	}
	return inv
}

// createIntent simulates the payer opening the link and choosing a method. Anonymous by
// default: SRS section 2.4 allows a payer with no account, and that is the common path.
func createIntent(t *testing.T, c *client, paymentToken, method string) intentResponse {
	t.Helper()
	r := c.post(t, "/api/v1/pay/"+paymentToken, map[string]any{"method": method})
	requireStatus(t, r, http.StatusCreated)

	intent := decode[intentResponse](t, r)
	if intent.Status != "PENDING" {
		t.Fatalf("new intent status = %q, want PENDING — an intent must never settle itself", intent.Status)
	}
	return intent
}

// settleIntent is the admin action. Returns the raw response so callers can assert on
// rejection as well as success.
func settleIntent(t *testing.T, intentID, action string) *response {
	t.Helper()
	return adminClient().patch(t, "/api/v1/admin/payments/"+intentID, map[string]any{"action": action})
}

// paidInvoice runs the whole payer journey and leaves the invoice PAID with the merchant
// credited. Verified end to end so a scenario that merely *needs* a paid invoice fails on
// its own subject rather than on broken setup.
func paidInvoice(t *testing.T, m *merchant, amount int64) invoiceResponse {
	t.Helper()

	inv := m.createInvoice(t, amount)
	intent := createIntent(t, newClient(), inv.PaymentToken, "VA_DUMMY")
	requireStatus(t, settleIntent(t, intent.ID, "SUCCESS"), http.StatusOK)

	if got := publicStatus(t, inv.PaymentToken); got != "PAID" {
		t.Fatalf("setup: invoice status = %q after a successful payment, want PAID", got)
	}
	return inv
}

func publicStatus(t *testing.T, paymentToken string) string {
	t.Helper()
	r := newClient().get(t, "/api/v1/pay/"+paymentToken)
	requireStatus(t, r, http.StatusOK)
	return decode[publicInvoiceResponse](t, r).Status
}

// requestRefund returns the raw response: most refund scenarios are about the request
// being *refused*, so the caller decides what a pass looks like.
func (m *merchant) requestRefund(t *testing.T, invoiceID string, amount int64) *response {
	t.Helper()
	return m.post(t, "/api/v1/refunds", map[string]any{
		"invoice_id": invoiceID,
		"amount":     amount,
		"reason":     "e2e scenario",
	})
}

func (m *merchant) mustRequestRefund(t *testing.T, invoiceID string, amount int64) refundResponse {
	t.Helper()
	r := m.requestRefund(t, invoiceID, amount)
	requireStatus(t, r, http.StatusCreated)

	rf := decode[refundResponse](t, r)
	if rf.Status != "REQUESTED" {
		t.Fatalf("new refund status = %q, want REQUESTED", rf.Status)
	}
	return rf
}

func actOnRefund(t *testing.T, refundID, action string) *response {
	t.Helper()
	return adminClient().patch(t, "/api/v1/admin/refunds/"+refundID, map[string]any{"action": action})
}

func mustActOnRefund(t *testing.T, refundID, action string) refundResponse {
	t.Helper()
	r := actOnRefund(t, refundID, action)
	requireStatus(t, r, http.StatusOK)
	return decode[refundResponse](t, r)
}

// topup requests a top-up, which is always PENDING — SRS section 2.2 forbids crediting on
// request. Asserted here so the invariant is checked on every use, not only where a test
// happens to look.
func (m *merchant) topup(t *testing.T, amount int64) topupResponse {
	t.Helper()
	r := m.post(t, "/api/v1/wallet/topup", map[string]any{"amount": amount})
	requireStatus(t, r, http.StatusCreated)

	tu := decode[topupResponse](t, r)
	if tu.Status != "PENDING" {
		t.Fatalf("new topup status = %q, want PENDING — a top-up must never credit on request", tu.Status)
	}
	return tu
}

func settleTopup(t *testing.T, topupID, action string) *response {
	t.Helper()
	return adminClient().patch(t, "/api/v1/admin/topups/"+topupID, map[string]any{"action": action})
}

// fundedMerchant returns a merchant whose wallet holds exactly amount, via the top-up
// path. Used by scenarios that need a payer able to spend from a wallet.
func fundedMerchant(t *testing.T, amount int64) *merchant {
	t.Helper()

	m := newMerchant(t)
	tu := m.topup(t, amount)
	requireStatus(t, settleTopup(t, tu.ID, "SUCCESS"), http.StatusOK)

	if got := m.balance(t); got != amount {
		t.Fatalf("setup: balance = %d after an approved top-up of %d", got, amount)
	}
	return m
}
