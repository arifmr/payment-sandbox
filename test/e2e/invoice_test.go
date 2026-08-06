//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestE2E_Invoice_CreationGeneratesItsIdentifiers checks the two server-generated values
// and their different jobs: invoice_number is a human-readable identifier, payment_token is
// the only thing protecting the public page. Both must be unique, and the token must be
// long enough that guessing is hopeless.
func TestE2E_Invoice_CreationGeneratesItsIdentifiers(t *testing.T) {
	m := newMerchant(t)

	const runs = 5
	numbers := map[string]bool{}
	tokens := map[string]bool{}

	for i := 0; i < runs; i++ {
		inv := m.createInvoice(t, int64(10_000+i))

		if numbers[inv.InvoiceNumber] {
			t.Fatalf("invoice_number %q was issued twice", inv.InvoiceNumber)
		}
		if tokens[inv.PaymentToken] {
			t.Fatalf("payment_token %q was issued twice", inv.PaymentToken)
		}
		numbers[inv.InvoiceNumber] = true
		tokens[inv.PaymentToken] = true

		// 32 random bytes rendered as hex. Shorter would make the public page guessable.
		if len(inv.PaymentToken) != 64 {
			t.Errorf("payment_token is %d characters, want 64 (32 bytes hex)", len(inv.PaymentToken))
		}
		if !strings.HasPrefix(inv.InvoiceNumber, "INV-") {
			t.Errorf("invoice_number = %q, want the documented INV- prefix", inv.InvoiceNumber)
		}
		if inv.PaymentLink == "" || !strings.Contains(inv.PaymentLink, inv.PaymentToken) {
			t.Errorf("payment_link = %q, want a link containing the token", inv.PaymentLink)
		}
	}
}

// TestE2E_Invoice_ValidatesItsInput exercises the binding tags plus the due-date rule.
// SRS section 4.5 requires amount > 0 and a due date not in the past; the frontend checks
// both for UX, but only the server's answer is binding.
func TestE2E_Invoice_ValidatesItsInput(t *testing.T) {
	m := newMerchant(t)
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"zero amount", map[string]any{"customer_name": "A", "amount": 0, "due_date": future}},
		{"negative amount", map[string]any{"customer_name": "A", "amount": -5_000, "due_date": future}},
		{"missing customer name", map[string]any{"amount": 1_000, "due_date": future}},
		{"missing due date", map[string]any{"customer_name": "A", "amount": 1_000}},
		{"due date in the past", map[string]any{
			"customer_name": "A", "amount": 1_000,
			"due_date": time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano),
		}},
		{"malformed customer email", map[string]any{
			"customer_name": "A", "customer_email": "nope", "amount": 1_000, "due_date": future,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := m.post(t, "/api/v1/invoices", tc.body)
			if resp.Status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400\nbody: %s", resp.Status, resp.Body)
			}
		})
	}
}

// TestE2E_Invoice_ListIsScopedFilteredAndPaginated covers the list contract in one pass:
// scoping by token, filtering by status, and the pagination envelope.
func TestE2E_Invoice_ListIsScopedFilteredAndPaginated(t *testing.T) {
	mine := newMerchant(t)
	theirs := newMerchant(t)

	theirInvoice := theirs.createInvoice(t, 99_000)

	// Three pending, one paid — enough to tell filtering from luck.
	for i := 0; i < 3; i++ {
		mine.createInvoice(t, int64(20_000+i))
	}
	paid := paidInvoice(t, mine, 55_000)

	t.Run("scoped to the caller", func(t *testing.T) {
		page := listInvoices(t, mine, "?page=1&page_size=100")
		if page.Pagination.Total != 4 {
			t.Errorf("total = %d, want 4 — the list is not scoped to the caller", page.Pagination.Total)
		}
		for _, inv := range page.Data {
			if inv.ID == theirInvoice.ID {
				t.Fatal("the list contained another merchant's invoice")
			}
		}
	})

	t.Run("filtered by status", func(t *testing.T) {
		page := listInvoices(t, mine, "?status=PAID&page=1&page_size=100")
		if page.Pagination.Total != 1 {
			t.Errorf("total = %d for status=PAID, want 1", page.Pagination.Total)
		}
		for _, inv := range page.Data {
			if inv.Status != "PAID" {
				t.Errorf("status filter returned an invoice with status %q", inv.Status)
			}
		}
		if len(page.Data) == 1 && page.Data[0].ID != paid.ID {
			t.Error("status=PAID returned the wrong invoice")
		}
	})

	t.Run("paginated", func(t *testing.T) {
		first := listInvoices(t, mine, "?page=1&page_size=2")
		if len(first.Data) != 2 {
			t.Errorf("page 1 returned %d rows, want 2", len(first.Data))
		}
		if first.Pagination.Total != 4 {
			t.Errorf("total = %d, want 4 — total must count matches, not the page", first.Pagination.Total)
		}

		second := listInvoices(t, mine, "?page=2&page_size=2")
		if len(second.Data) != 2 {
			t.Errorf("page 2 returned %d rows, want 2", len(second.Data))
		}

		// Pages must not overlap, or the client sees duplicates while scrolling.
		onFirst := map[string]bool{}
		for _, inv := range first.Data {
			onFirst[inv.ID] = true
		}
		for _, inv := range second.Data {
			if onFirst[inv.ID] {
				t.Errorf("invoice %s appeared on both page 1 and page 2", inv.InvoiceNumber)
			}
		}
	})
}

// TestE2E_Invoice_PageSizeIsCapped pins the limit that stops `?page_size=1000000` from
// being a way to read a whole table in one request. The cap lives in
// pagination.FromQuery, so it applies to every list endpoint at once.
func TestE2E_Invoice_PageSizeIsCapped(t *testing.T) {
	m := newMerchant(t)
	m.createInvoice(t, 1_000)

	for _, qs := range []string{"?page_size=1000000", "?page_size=101", "?page_size=999"} {
		t.Run(qs, func(t *testing.T) {
			page := listInvoices(t, m, qs)
			if page.Pagination.PageSize > 100 {
				t.Errorf("page_size = %d, want it clamped to at most 100", page.Pagination.PageSize)
			}
		})
	}
}

// TestE2E_Invoice_MalformedPaginationFallsBackToDefaults checks bad input degrades to
// something sensible rather than 500ing or returning an unbounded page.
func TestE2E_Invoice_MalformedPaginationFallsBackToDefaults(t *testing.T) {
	m := newMerchant(t)
	m.createInvoice(t, 1_000)

	for _, qs := range []string{"?page=0", "?page=-3", "?page=abc", "?page_size=0", "?page_size=-10", "?page_size=xyz"} {
		t.Run(qs, func(t *testing.T) {
			r := m.get(t, "/api/v1/invoices"+qs)
			requireStatus(t, r, http.StatusOK)

			page := decode[paginated[invoiceResponse]](t, r)
			if page.Pagination.Page < 1 {
				t.Errorf("page = %d, want at least 1", page.Pagination.Page)
			}
			if page.Pagination.PageSize < 1 || page.Pagination.PageSize > 100 {
				t.Errorf("page_size = %d, want between 1 and 100", page.Pagination.PageSize)
			}
		})
	}
}

// TestE2E_Invoice_DetailBelongsToItsOwner checks the merchant-facing detail view returns
// the full record — including the fields the public view withholds.
func TestE2E_Invoice_DetailBelongsToItsOwner(t *testing.T) {
	m := newMerchant(t)
	created := m.createInvoice(t, 42_000)

	r := m.get(t, "/api/v1/invoices/"+created.ID)
	requireStatus(t, r, http.StatusOK)
	got := decode[invoiceResponse](t, r)

	if got.ID != created.ID || got.InvoiceNumber != created.InvoiceNumber {
		t.Errorf("detail returned a different invoice: %+v", got)
	}
	if got.MerchantID != m.ID {
		t.Errorf("merchant_id = %q, want %q", got.MerchantID, m.ID)
	}
	// The owner is entitled to these; only the public view hides them.
	if got.PaymentToken == "" {
		t.Error("the owner's own detail view withheld payment_token, so the payment link cannot be shown")
	}
	if got.CustomerEmail == "" {
		t.Error("the owner's own detail view withheld customer_email")
	}
}

func listInvoices(t *testing.T, m *merchant, query string) paginated[invoiceResponse] {
	t.Helper()
	r := m.get(t, "/api/v1/invoices"+query)
	requireStatus(t, r, http.StatusOK)
	return decode[paginated[invoiceResponse]](t, r)
}
