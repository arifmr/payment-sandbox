package constant

type PaymentMethod string

const (
	PaymentMethodWallet       PaymentMethod = "WALLET"
	PaymentMethodVADummy      PaymentMethod = "VA_DUMMY"
	PaymentMethodEwalletDummy PaymentMethod = "EWALLET_DUMMY"
)

func (m PaymentMethod) Valid() bool {
	switch m {
	case PaymentMethodWallet, PaymentMethodVADummy, PaymentMethodEwalletDummy:
		return true
	}
	return false
}

type PaymentIntentStatus string

const (
	PaymentPending PaymentIntentStatus = "PENDING"
	PaymentSuccess PaymentIntentStatus = "SUCCESS"
	PaymentFailed  PaymentIntentStatus = "FAILED"
)
