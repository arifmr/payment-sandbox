package constant

type TopupStatus string

const (
	TopupPending TopupStatus = "PENDING"
	TopupSuccess TopupStatus = "SUCCESS"
	TopupFailed  TopupStatus = "FAILED"
)
