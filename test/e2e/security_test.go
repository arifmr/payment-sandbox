//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestE2E_Security_RoleMatrix tries every protected route with both a merchant token and
// an admin token.
//
// Testing both directions is what makes it a matrix rather than a spot check. The obvious
// risk is an admin route reachable by a merchant; the quieter one is a merchant route left
// open to admins, which a per-endpoint test written from the admin's side never looks for.
func TestE2E_Security_RoleMatrix(t *testing.T) {
	merchantToken := newMerchant(t).token

	cases := []struct {
		method     string
		path       string
		merchantOK bool
		adminOK    bool
	}{
		{http.MethodGet, "/api/v1/wallet", true, false},
		{http.MethodGet, "/api/v1/wallet/topups", true, false},
		{http.MethodGet, "/api/v1/invoices", true, false},
		{http.MethodGet, "/api/v1/refunds", true, false},

		{http.MethodGet, "/api/v1/admin/dashboard", false, true},
		{http.MethodGet, "/api/v1/admin/refunds", false, true},
		{http.MethodGet, "/api/v1/admin/topups", false, true},
		{http.MethodGet, "/api/v1/admin/payments", false, true},
	}

	for _, tc := range cases {
		t.Run(strings.TrimPrefix(tc.path, "/api/v1/"), func(t *testing.T) {
			for _, who := range []struct {
				label string
				token string
				want  bool
			}{
				{"merchant", merchantToken, tc.merchantOK},
				{"admin", adminToken, tc.adminOK},
			} {
				resp := newClient().as(who.token).do(t, tc.method, tc.path, nil)

				if who.want && resp.Status == http.StatusForbidden {
					t.Errorf("%s got 403 on %s %s but should be allowed", who.label, tc.method, tc.path)
				}
				if !who.want && resp.Status != http.StatusForbidden {
					t.Errorf("%s got %d on %s %s, want 403\nbody: %s",
						who.label, resp.Status, tc.method, tc.path, resp.Body)
				}
			}
		})
	}
}

// TestE2E_Security_ForbiddenActionsHaveNoSideEffect is the assertion a status-code check
// misses. A 403 returned after the work already happened is still a breach, so the
// wallet — the thing the action would have moved — is checked too.
func TestE2E_Security_ForbiddenActionsHaveNoSideEffect(t *testing.T) {
	m := newMerchant(t)
	inv := m.createInvoice(t, 45_000)
	intent := createIntent(t, newClient(), inv.PaymentToken, "VA_DUMMY")

	// A merchant trying to mark its own payment SUCCESS: the most tempting escalation,
	// since it would credit their own wallet.
	resp := m.patch(t, "/api/v1/admin/payments/"+intent.ID, map[string]any{"action": "SUCCESS"})
	requireStatus(t, resp, http.StatusForbidden)

	if got := m.balance(t); got != 0 {
		t.Errorf("balance = %d after a 403, want 0 — the forbidden action still ran", got)
	}
	if got := publicStatus(t, inv.PaymentToken); got != "PENDING" {
		t.Errorf("invoice status = %q after a 403, want PENDING", got)
	}
}

// TestE2E_Security_ProtectedRoutesRejectMissingAndBrokenTokens checks the auth middleware
// fails closed on every shape of bad credential.
func TestE2E_Security_ProtectedRoutesRejectMissingAndBrokenTokens(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"empty bearer", "Bearer "},
		{"garbage token", "Bearer not-a-jwt"},
		{"missing scheme", "just-a-token"},
		{"wrong scheme", "Basic dXNlcjpwYXNz"},
		{"structurally valid but forged", "Bearer " + forgedHS256()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient()
			var resp *response
			if tc.header == "" {
				resp = c.get(t, "/api/v1/wallet")
			} else {
				resp = c.get(t, "/api/v1/wallet", withHeader("Authorization", tc.header))
			}
			if resp.Status != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401\nbody: %s", resp.Status, resp.Body)
			}
		})
	}
}

// TestE2E_Security_UnsignedTokenIsRejected is the alg=none downgrade.
//
// This vulnerability is invisible on every happy path — a service with it wide open behaves
// perfectly for legitimate traffic. The only way to know is to attack it, so the test
// builds an unsigned token claiming ADMIN and checks the door stays shut. The claims are
// crafted from scratch here; no secret is involved, which is exactly the point.
func TestE2E_Security_UnsignedTokenIsRejected(t *testing.T) {
	token := unsignedAdminToken()

	for _, path := range []string{"/api/v1/admin/dashboard", "/api/v1/wallet"} {
		t.Run(strings.TrimPrefix(path, "/api/v1/"), func(t *testing.T) {
			resp := newClient().get(t, path, withHeader("Authorization", "Bearer "+token))
			if resp.Status != http.StatusUnauthorized {
				t.Fatalf("an alg=none token claiming ADMIN got %d on %s, want 401\nbody: %s",
					resp.Status, path, resp.Body)
			}
		})
	}
}

// TestE2E_Security_ClientSuppliedOwnershipIsIgnored is the mass-assignment check. Identity
// must come from the token; if the body could set merchant_id, anyone could issue invoices
// in someone else's name. Status is included because a client-chosen PAID would be a free
// invoice.
func TestE2E_Security_ClientSuppliedOwnershipIsIgnored(t *testing.T) {
	victim := newMerchant(t)
	attacker := newMerchant(t)

	r := attacker.post(t, "/api/v1/invoices", map[string]any{
		"customer_name": "Budi",
		"amount":        10_000,
		"due_date":      time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano),
		// Everything below is an attempt to override server-owned state.
		"merchant_id":   victim.ID,
		"status":        "PAID",
		"payment_token": "attacker-chosen-token",
		"id":            "00000000-0000-0000-0000-000000000001",
	})
	requireStatus(t, r, http.StatusCreated)
	inv := decode[invoiceResponse](t, r)

	if inv.MerchantID == victim.ID {
		t.Error("merchant_id was taken from the request body; an invoice was created for another merchant")
	}
	if inv.MerchantID != attacker.ID {
		t.Errorf("merchant_id = %q, want the caller's id %q (from the token)", inv.MerchantID, attacker.ID)
	}
	if inv.Status != "PENDING" {
		t.Errorf("status = %q, want PENDING — a client must not be able to open an invoice as PAID", inv.Status)
	}
	if inv.PaymentToken == "attacker-chosen-token" {
		t.Error("payment_token was taken from the request body; the payer-facing secret must be server-generated")
	}

	// The victim's ledger is untouched.
	if got := victim.balance(t); got != 0 {
		t.Errorf("victim balance = %d, want 0", got)
	}
}

// TestE2E_Security_PublicPaymentPageHidesInternalFields asserts on the raw JSON, because
// the question is what actually crosses the wire.
//
// payment_token is the one that matters most: a page echoing its own token would leak it
// into browser history, proxy logs and Referer headers — and that token is the only thing
// guarding the page.
func TestE2E_Security_PublicPaymentPageHidesInternalFields(t *testing.T) {
	m := newMerchant(t)
	inv := m.createInvoice(t, 15_000)

	r := newClient().get(t, "/api/v1/pay/"+inv.PaymentToken)
	requireStatus(t, r, http.StatusOK)
	body := string(r.Body)

	for _, forbidden := range []struct{ label, needle string }{
		{"the payment token itself", inv.PaymentToken},
		{"a payment_token field", "payment_token"},
		{"the customer email", "budi@example.com"},
		{"a customer_email field", "customer_email"},
		{"the merchant id", inv.MerchantID},
		{"a merchant_id field", "merchant_id"},
	} {
		if strings.Contains(body, forbidden.needle) {
			t.Errorf("the public payment response exposes %s\nbody: %s", forbidden.label, body)
		}
	}

	// It still carries what the page genuinely needs.
	pub := decode[publicInvoiceResponse](t, r)
	if pub.Amount != 15_000 || pub.InvoiceNumber == "" || pub.MerchantName == "" {
		t.Errorf("public response is missing fields the payment page needs: %+v", pub)
	}
}

// TestE2E_Security_MerchantCannotReadForeignInvoice is the read-side ownership check.
// 404 rather than 403 is the right answer: 403 would confirm the id exists.
func TestE2E_Security_MerchantCannotReadForeignInvoice(t *testing.T) {
	victim := newMerchant(t)
	attacker := newMerchant(t)

	inv := victim.createInvoice(t, 33_000)

	resp := attacker.get(t, "/api/v1/invoices/"+inv.ID)
	if resp.Status == http.StatusOK {
		t.Fatal("a merchant read another merchant's invoice")
	}
	if resp.Status != http.StatusNotFound && resp.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 404 or 403\nbody: %s", resp.Status, resp.Body)
	}
}

// TestE2E_Security_ErrorsUseTheDocumentedEnvelope keeps the failure contract stable. A
// client that branches on error.code needs every error to have one, and a leaked driver
// message would both break that shape and describe the schema to an attacker.
func TestE2E_Security_ErrorsUseTheDocumentedEnvelope(t *testing.T) {
	m := newMerchant(t)

	cases := []struct {
		name string
		call func() *response
	}{
		{"unknown invoice", func() *response {
			return m.get(t, "/api/v1/invoices/00000000-0000-0000-0000-000000000000")
		}},
		{"malformed uuid", func() *response { return m.get(t, "/api/v1/invoices/not-a-uuid") }},
		{"unknown payment token", func() *response {
			return newClient().get(t, "/api/v1/pay/"+strings.Repeat("0", 64))
		}},
		{"invalid payment method", func() *response {
			inv := m.createInvoice(t, 1_000)
			return newClient().post(t, "/api/v1/pay/"+inv.PaymentToken, map[string]any{"method": "BITCOIN"})
		}},
		{"unauthenticated", func() *response { return newClient().get(t, "/api/v1/wallet") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := tc.call()
			if resp.Status < 400 {
				t.Fatalf("expected an error, got %d\nbody: %s", resp.Status, resp.Body)
			}

			env := decode[errorEnvelope](t, resp)
			if env.Error.Code == "" || env.Error.Message == "" {
				t.Errorf("error is missing code or message: %s", resp.Body)
			}
			if resp.Status >= 500 {
				t.Errorf("status = %d — an anticipated condition must not be a 5xx\nbody: %s", resp.Status, resp.Body)
			}

			// Driver and schema detail must never reach the client.
			body := strings.ToLower(string(resp.Body))
			for _, leak := range []string{"pq:", "pgx", "sql:", "sqlstate", "panic", "goroutine", "/users/", "postgres://"} {
				if strings.Contains(body, leak) {
					t.Errorf("error body leaks internal detail %q: %s", leak, resp.Body)
				}
			}
		})
	}
}

// ---------- token forgery helpers ----------

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// unsignedAdminToken builds `alg: none` with an empty signature — the classic downgrade a
// library that trusts the header will happily accept.
func unsignedAdminToken() string {
	header, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"sub":  "00000000-0000-0000-0000-000000000001",
		"role": "ADMIN",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	})
	return b64url(header) + "." + b64url(claims) + "."
}

// forgedHS256 claims HS256 and carries a signature made up out of nothing. It should fail
// verification — unlike the alg=none case, which fails on the algorithm check first.
func forgedHS256() string {
	header, _ := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"sub":  "00000000-0000-0000-0000-000000000002",
		"role": "ADMIN",
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	return b64url(header) + "." + b64url(claims) + "." + b64url([]byte("not-a-real-signature"))
}
