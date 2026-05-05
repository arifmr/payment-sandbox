package constant

type InvoiceStatus string

const (
	InvoicePending InvoiceStatus = "PENDING"
	InvoicePaid    InvoiceStatus = "PAID"
	InvoiceExpired InvoiceStatus = "EXPIRED"
)
