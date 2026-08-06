package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
)

// The mappers are the boundary between internal entities and the wire. Their job
// is as much about what they omit as what they include (SRS §5.2).

func TestToUser_OmitsPasswordHash(t *testing.T) {
	u := &User{
		ID: uuid.New(), Email: "toko@example.com", Name: "Toko A",
		Role: constant.RoleMerchant, PasswordHash: "$2a$12$averysecrethash",
	}

	got := ToUser(u)
	if got.ID != u.ID.String() || got.Email != u.Email || got.Name != u.Name {
		t.Errorf("fields not copied: %+v", got)
	}
	if got.Role != string(constant.RoleMerchant) {
		t.Errorf("role = %q", got.Role)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "averysecrethash") {
		t.Fatalf("user response leaks the password hash: %s", raw)
	}
}

func TestToWallet(t *testing.T) {
	now := time.Now().UTC()
	w := &Wallet{ID: uuid.New(), MerchantID: uuid.New(), Balance: 12345, Version: 9, UpdatedAt: now}

	got := ToWallet(w)
	if got.MerchantID != w.MerchantID.String() || got.Balance != 12345 || !got.UpdatedAt.Equal(now) {
		t.Errorf("unexpected mapping: %+v", got)
	}

	// The optimistic-lock counter is an internal detail.
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "version") {
		t.Errorf("wallet response exposes the internal version counter: %s", raw)
	}
}

func TestToInvoice_PaymentLinkComposition(t *testing.T) {
	inv := &Invoice{
		ID: uuid.New(), InvoiceNumber: "INV-20260726-ABCDEF0123", MerchantID: uuid.New(),
		CustomerName: "Budi", Amount: 50000, Status: constant.InvoicePending,
		DueDate: time.Now().Add(time.Hour), PaymentToken: "tok-xyz",
	}

	withBase := ToInvoice(inv, "/api/v1/pay")
	if withBase.PaymentLink != "/api/v1/pay/tok-xyz" {
		t.Errorf("payment_link = %q", withBase.PaymentLink)
	}

	// With no base the link is omitted rather than emitted as a bare token.
	withoutBase := ToInvoice(inv, "")
	if withoutBase.PaymentLink != "" {
		t.Errorf("payment_link = %q, want empty when no base is configured", withoutBase.PaymentLink)
	}
	raw, _ := json.Marshal(withoutBase)
	if strings.Contains(string(raw), "payment_link") {
		t.Errorf("empty payment_link should be omitted: %s", raw)
	}
}

func TestToInvoice_PaidAtOmittedWhenUnpaid(t *testing.T) {
	inv := &Invoice{ID: uuid.New(), MerchantID: uuid.New(), Status: constant.InvoicePending}

	raw, err := json.Marshal(ToInvoice(inv, ""))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "paid_at") {
		t.Errorf("paid_at should be omitted while the invoice is unpaid: %s", raw)
	}

	paidAt := time.Now().UTC()
	inv.Status = constant.InvoicePaid
	inv.PaidAt = &paidAt
	raw, _ = json.Marshal(ToInvoice(inv, ""))
	if !strings.Contains(string(raw), "paid_at") {
		t.Errorf("paid_at must be present once the invoice is PAID: %s", raw)
	}
}

// SRS §4.3: the public payment page shows only what the payer needs.
func TestToPublicInvoice_ExposesOnlyPayerFacingFields(t *testing.T) {
	paidAt := time.Now().UTC()
	inv := &Invoice{
		ID: uuid.New(), InvoiceNumber: "INV-1", MerchantID: uuid.New(),
		CustomerName: "Budi", CustomerEmail: "budi@example.com",
		Description: "Order #1", Amount: 50000, Status: constant.InvoicePending,
		DueDate: time.Now().Add(time.Hour), PaymentToken: "tok-secret", PaidAt: &paidAt,
	}

	got := ToPublicInvoice(inv, "Toko A")
	if got.MerchantName != "Toko A" || got.InvoiceNumber != "INV-1" || got.Amount != 50000 {
		t.Errorf("unexpected mapping: %+v", got)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leaked := range []string{"tok-secret", "budi@example.com", inv.MerchantID.String(), inv.ID.String()} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("public invoice leaks %q: %s", leaked, raw)
		}
	}
}

func TestToPaymentIntent(t *testing.T) {
	payer := uuid.New()
	processed := time.Now().UTC()
	p := &PaymentIntent{
		ID: uuid.New(), InvoiceID: uuid.New(), Method: constant.PaymentMethodWallet,
		Status: constant.PaymentSuccess, Amount: 5000, PayerUserID: &payer,
		CreatedAt: time.Now().UTC(), ProcessedAt: &processed,
	}

	got := ToPaymentIntent(p)
	if got.ID != p.ID.String() || got.InvoiceID != p.InvoiceID.String() {
		t.Errorf("ids not copied: %+v", got)
	}
	if got.Method != string(constant.PaymentMethodWallet) || got.Status != string(constant.PaymentSuccess) {
		t.Errorf("method/status not copied: %+v", got)
	}
	if got.ProcessedAt == nil || !got.ProcessedAt.Equal(processed) {
		t.Errorf("processed_at = %v, want %v", got.ProcessedAt, processed)
	}

	// The payer's identity is not part of the payer-visible contract.
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), payer.String()) {
		t.Errorf("payment intent response exposes the payer id: %s", raw)
	}
}

func TestToPaymentIntent_ProcessedAtOmittedWhenPending(t *testing.T) {
	p := &PaymentIntent{ID: uuid.New(), InvoiceID: uuid.New(), Status: constant.PaymentPending}

	raw, _ := json.Marshal(ToPaymentIntent(p))
	if strings.Contains(string(raw), "processed_at") {
		t.Errorf("processed_at should be omitted while PENDING: %s", raw)
	}
}

func TestToRefund(t *testing.T) {
	processed := time.Now().UTC()
	r := &Refund{
		ID: uuid.New(), InvoiceID: uuid.New(), PaymentIntentID: uuid.New(), MerchantID: uuid.New(),
		Amount: 1500, Reason: "customer cancelled", Status: constant.RefundSuccess,
		CreatedAt: time.Now().UTC(), ProcessedAt: &processed,
	}

	got := ToRefund(r)
	if got.ID != r.ID.String() || got.InvoiceID != r.InvoiceID.String() ||
		got.PaymentIntentID != r.PaymentIntentID.String() || got.MerchantID != r.MerchantID.String() {
		t.Errorf("ids not copied: %+v", got)
	}
	if got.Amount != 1500 || got.Reason != "customer cancelled" || got.Status != string(constant.RefundSuccess) {
		t.Errorf("fields not copied: %+v", got)
	}
}

func TestToTopup(t *testing.T) {
	processed := time.Now().UTC()
	tp := &Topup{
		ID: uuid.New(), MerchantID: uuid.New(), Amount: 25000,
		Status: constant.TopupSuccess, CreatedAt: time.Now().UTC(), ProcessedAt: &processed,
	}

	got := ToTopup(tp)
	if got.ID != tp.ID.String() || got.MerchantID != tp.MerchantID.String() {
		t.Errorf("ids not copied: %+v", got)
	}
	if got.Amount != 25000 || got.Status != string(constant.TopupSuccess) {
		t.Errorf("fields not copied: %+v", got)
	}
	if got.ProcessedAt == nil {
		t.Error("processed_at must be carried through for a settled top-up")
	}
}

// ── RefreshToken.IsActive ─────────────────────────────────────────────────────

func TestRefreshToken_IsActive(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	revoked := now.Add(-time.Hour)

	cases := []struct {
		name  string
		token RefreshToken
		want  bool
	}{
		{"valid and unrevoked", RefreshToken{ExpiresAt: now.Add(time.Hour)}, true},
		{"expired", RefreshToken{ExpiresAt: now.Add(-time.Hour)}, false},
		{"expiring exactly now", RefreshToken{ExpiresAt: now}, false},
		{"revoked but unexpired", RefreshToken{ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked}, false},
		{"revoked and expired", RefreshToken{ExpiresAt: now.Add(-time.Hour), RevokedAt: &revoked}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.token.IsActive(now); got != tc.want {
				t.Errorf("IsActive() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The error envelope is part of the public contract (README).
func TestErrorResponse_WireShape(t *testing.T) {
	raw, err := json.Marshal(ErrorResponse{Error: ErrorBody{Code: "INVALID_STATE", Message: "invalid state transition"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"error":{"code":"INVALID_STATE","message":"invalid state transition"}}`
	if string(raw) != want {
		t.Errorf("error envelope = %s, want %s", raw, want)
	}
}
