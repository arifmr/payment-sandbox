package hash

import "golang.org/x/crypto/bcrypt"

type Hasher struct {
	cost int
	// decoy is a valid bcrypt hash at the configured cost, used by CompareDecoy to
	// spend the same time as a real verification. See CompareDecoy.
	decoy []byte
}

func New(cost int) *Hasher {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = bcrypt.DefaultCost
	}
	h := &Hasher{cost: cost}
	// Generated once at construction so the first failed login does not pay a
	// one-off cost that itself leaks timing. Cost is already clamped above, so the
	// only realistic error source is gone; on failure CompareDecoy degrades to a
	// no-op rather than breaking startup.
	if d, err := bcrypt.GenerateFromPassword([]byte("decoy-password"), h.cost); err == nil {
		h.decoy = d
	}
	return h
}

func (h *Hasher) Hash(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (h *Hasher) Compare(hashed, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain))
}

// CompareDecoy performs a bcrypt verification against a throwaway hash and discards
// the result.
//
// Login must not reveal whether an account exists. Returning the same error message
// is not enough: a missing account would otherwise skip bcrypt entirely and answer in
// microseconds, while an existing one spends hundreds of milliseconds. That gap is
// easily measurable over a few requests, so callers invoke this on the
// account-not-found path to make both branches cost the same.
func (h *Hasher) CompareDecoy() {
	if h.decoy == nil {
		return
	}
	_ = bcrypt.CompareHashAndPassword(h.decoy, []byte("decoy-password-attempt"))
}
