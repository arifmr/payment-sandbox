package constant

import "github.com/dboarif/payment-sandbox/internal/pkg/statemachine"

// The four entity state machines from the SRS (§3.4). Services consult these
// before asking a repository to perform a CAS update, so the allowed graph lives
// in exactly one place instead of being re-spelled at every call site.
//
//	Invoice:       PENDING -> PAID | EXPIRED
//	PaymentIntent: PENDING -> SUCCESS | FAILED
//	Refund:        REQUESTED -> APPROVED | REJECTED ; APPROVED -> SUCCESS | FAILED
//	Topup:         PENDING -> SUCCESS | FAILED
var (
	InvoiceFSM = statemachine.New(map[InvoiceStatus][]InvoiceStatus{
		InvoicePending: {InvoicePaid, InvoiceExpired},
	})

	PaymentFSM = statemachine.New(map[PaymentIntentStatus][]PaymentIntentStatus{
		PaymentPending: {PaymentSuccess, PaymentFailed},
	})

	RefundFSM = statemachine.New(map[RefundStatus][]RefundStatus{
		RefundRequested: {RefundApproved, RefundRejected},
		RefundApproved:  {RefundSuccess, RefundFailed},
	})

	TopupFSM = statemachine.New(map[TopupStatus][]TopupStatus{
		TopupPending: {TopupSuccess, TopupFailed},
	})
)

// Valid reports whether s is a known invoice status. Used to reject bogus
// values in list filters before they reach SQL.
func (s InvoiceStatus) Valid() bool {
	switch s {
	case InvoicePending, InvoicePaid, InvoiceExpired:
		return true
	}
	return false
}

// Valid reports whether s is a known payment intent status.
func (s PaymentIntentStatus) Valid() bool {
	switch s {
	case PaymentPending, PaymentSuccess, PaymentFailed:
		return true
	}
	return false
}

// Valid reports whether s is a known refund status.
func (s RefundStatus) Valid() bool {
	switch s {
	case RefundRequested, RefundApproved, RefundRejected, RefundSuccess, RefundFailed:
		return true
	}
	return false
}

// Valid reports whether s is a known topup status.
func (s TopupStatus) Valid() bool {
	switch s {
	case TopupPending, TopupSuccess, TopupFailed:
		return true
	}
	return false
}
