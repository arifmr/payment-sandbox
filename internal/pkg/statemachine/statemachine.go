package statemachine

// FSM is a generic finite state machine helper. The "state" type is parameterized
// so each domain entity (Invoice, PaymentIntent, Refund, Topup) can use its own
// string-based status type without losing type safety.
type FSM[S comparable] struct {
	transitions map[S]map[S]struct{}
}

func New[S comparable](allowed map[S][]S) *FSM[S] {
	t := make(map[S]map[S]struct{}, len(allowed))
	for from, tos := range allowed {
		set := make(map[S]struct{}, len(tos))
		for _, to := range tos {
			set[to] = struct{}{}
		}
		t[from] = set
	}
	return &FSM[S]{transitions: t}
}

func (f *FSM[S]) Can(from, to S) bool {
	set, ok := f.transitions[from]
	if !ok {
		return false
	}
	_, ok = set[to]
	return ok
}
