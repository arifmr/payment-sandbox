package constant

import "testing"

// The tables below mirror the state diagrams in the SRS (§3.4) verbatim. If a
// transition is ever added to the code without updating the spec, one of these fails.

func TestInvoiceFSM_MatchesSpec(t *testing.T) {
	allowed := map[InvoiceStatus][]InvoiceStatus{
		InvoicePending: {InvoicePaid, InvoiceExpired},
	}
	assertGraph(t, "Invoice",
		[]InvoiceStatus{InvoicePending, InvoicePaid, InvoiceExpired},
		allowed, InvoiceFSM.Can)
}

func TestPaymentFSM_MatchesSpec(t *testing.T) {
	allowed := map[PaymentIntentStatus][]PaymentIntentStatus{
		PaymentPending: {PaymentSuccess, PaymentFailed},
	}
	assertGraph(t, "PaymentIntent",
		[]PaymentIntentStatus{PaymentPending, PaymentSuccess, PaymentFailed},
		allowed, PaymentFSM.Can)
}

func TestRefundFSM_MatchesSpec(t *testing.T) {
	allowed := map[RefundStatus][]RefundStatus{
		RefundRequested: {RefundApproved, RefundRejected},
		RefundApproved:  {RefundSuccess, RefundFailed},
	}
	assertGraph(t, "Refund",
		[]RefundStatus{RefundRequested, RefundApproved, RefundRejected, RefundSuccess, RefundFailed},
		allowed, RefundFSM.Can)
}

func TestTopupFSM_MatchesSpec(t *testing.T) {
	allowed := map[TopupStatus][]TopupStatus{
		TopupPending: {TopupSuccess, TopupFailed},
	}
	assertGraph(t, "Topup",
		[]TopupStatus{TopupPending, TopupSuccess, TopupFailed},
		allowed, TopupFSM.Can)
}

// assertGraph checks every from×to pair: exactly the listed edges are permitted
// and everything else — including self-loops and reversals — is refused.
func assertGraph[S comparable](t *testing.T, entity string, states []S, allowed map[S][]S, can func(from, to S) bool) {
	t.Helper()

	want := map[[2]S]bool{}
	for from, tos := range allowed {
		for _, to := range tos {
			want[[2]S{from, to}] = true
		}
	}

	for _, from := range states {
		for _, to := range states {
			expected := want[[2]S{from, to}]
			if got := can(from, to); got != expected {
				t.Errorf("%s: Can(%v -> %v) = %v, want %v", entity, from, to, got, expected)
			}
		}
	}
}

// Refund cannot jump straight from REQUESTED to SUCCESS: approval is mandatory.
func TestRefundFSM_ApprovalIsMandatory(t *testing.T) {
	if RefundFSM.Can(RefundRequested, RefundSuccess) {
		t.Error("REQUESTED -> SUCCESS must not be allowed without APPROVE")
	}
	if RefundFSM.Can(RefundRejected, RefundApproved) {
		t.Error("REJECTED is terminal")
	}
}

// Terminal statuses must have no outgoing edge at all.
func TestTerminalStatesAreDeadEnds(t *testing.T) {
	for _, to := range []InvoiceStatus{InvoicePending, InvoicePaid, InvoiceExpired} {
		if InvoiceFSM.Can(InvoicePaid, to) {
			t.Errorf("PAID invoice must be terminal, but PAID -> %s is allowed", to)
		}
		if InvoiceFSM.Can(InvoiceExpired, to) {
			t.Errorf("EXPIRED invoice must be terminal, but EXPIRED -> %s is allowed", to)
		}
	}
}

func TestStatusValid(t *testing.T) {
	if !InvoicePaid.Valid() || InvoiceStatus("NOPE").Valid() {
		t.Error("InvoiceStatus.Valid is wrong")
	}
	if !PaymentSuccess.Valid() || PaymentIntentStatus("NOPE").Valid() {
		t.Error("PaymentIntentStatus.Valid is wrong")
	}
	if !RefundApproved.Valid() || RefundStatus("NOPE").Valid() {
		t.Error("RefundStatus.Valid is wrong")
	}
	if !TopupPending.Valid() || TopupStatus("NOPE").Valid() {
		t.Error("TopupStatus.Valid is wrong")
	}
	if InvoiceStatus("").Valid() || RefundStatus("").Valid() {
		t.Error("the empty status must never be valid")
	}
}

func TestRoleValid(t *testing.T) {
	if !RoleMerchant.Valid() || !RoleAdmin.Valid() {
		t.Error("MERCHANT and ADMIN must be valid roles")
	}
	for _, r := range []Role{"", "merchant", "SUPERUSER"} {
		if r.Valid() {
			t.Errorf("Role(%q) must not be valid", r)
		}
	}
}

// SRS §2.4 fixes the payable methods; nothing else may be accepted.
func TestPaymentMethodValid(t *testing.T) {
	for _, m := range []PaymentMethod{PaymentMethodWallet, PaymentMethodVADummy, PaymentMethodEwalletDummy} {
		if !m.Valid() {
			t.Errorf("PaymentMethod(%q) must be valid", m)
		}
	}
	for _, m := range []PaymentMethod{"", "wallet", "CREDIT_CARD", "VA"} {
		if m.Valid() {
			t.Errorf("PaymentMethod(%q) must not be valid", m)
		}
	}
}
