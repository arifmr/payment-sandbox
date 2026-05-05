package constant

type RefundStatus string

const (
	RefundRequested RefundStatus = "REQUESTED"
	RefundApproved  RefundStatus = "APPROVED"
	RefundRejected  RefundStatus = "REJECTED"
	RefundSuccess   RefundStatus = "SUCCESS"
	RefundFailed    RefundStatus = "FAILED"
)
