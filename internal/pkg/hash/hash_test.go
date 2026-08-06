package hash

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Use the cheapest legal cost so the suite stays fast; production cost comes from config.
func testHasher() *Hasher { return New(bcrypt.MinCost) }

// SRS §3.3: passwords must be stored hashed, never in plaintext.
func TestHasher_HashDoesNotLeakPlaintext(t *testing.T) {
	h := testHasher()
	const plain = "password123"

	hashed, err := h.Hash(plain)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if hashed == plain || strings.Contains(hashed, plain) {
		t.Fatal("hash output must not contain the plaintext password")
	}
	if !strings.HasPrefix(hashed, "$2") {
		t.Fatalf("expected a bcrypt hash, got %q", hashed)
	}
}

func TestHasher_CompareRoundTrip(t *testing.T) {
	h := testHasher()
	const plain = "correct horse battery staple"

	hashed, err := h.Hash(plain)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if err := h.Compare(hashed, plain); err != nil {
		t.Fatalf("Compare with the right password failed: %v", err)
	}
}

func TestHasher_CompareRejectsWrongPassword(t *testing.T) {
	h := testHasher()
	hashed, err := h.Hash("password123")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	for _, wrong := range []string{"", "password124", "Password123", "password123 "} {
		if err := h.Compare(hashed, wrong); err == nil {
			t.Errorf("Compare accepted the wrong password %q", wrong)
		}
	}
}

func TestHasher_SaltMakesHashesUnique(t *testing.T) {
	h := testHasher()
	first, err := h.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	second, err := h.Hash("same-password")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if first == second {
		t.Fatal("identical passwords must hash to different values (salting)")
	}
	// Both must still verify.
	if err := h.Compare(second, "same-password"); err != nil {
		t.Fatalf("second hash does not verify: %v", err)
	}
}

func TestHasher_CompareRejectsCorruptHash(t *testing.T) {
	h := testHasher()
	if err := h.Compare("not-a-bcrypt-hash", "password123"); err == nil {
		t.Fatal("Compare accepted a malformed stored hash")
	}
}

// An out-of-range cost must fall back to bcrypt's default rather than erroring
// at hash time — otherwise a typo'd BCRYPT_COST would break every registration.
func TestNew_ClampsInvalidCost(t *testing.T) {
	for _, cost := range []int{0, -5, bcrypt.MaxCost + 1} {
		h := New(cost)
		if h.cost != bcrypt.DefaultCost {
			t.Errorf("New(%d).cost = %d, want default %d", cost, h.cost, bcrypt.DefaultCost)
		}
	}
}

func TestNew_KeepsValidCost(t *testing.T) {
	h := New(bcrypt.MinCost + 1)
	if h.cost != bcrypt.MinCost+1 {
		t.Errorf("cost = %d, want %d", h.cost, bcrypt.MinCost+1)
	}
}

// bcrypt silently truncates beyond 72 bytes; the register DTO caps password at 72,
// so document that the boundary still works.
func TestHasher_LongPasswordAtLimit(t *testing.T) {
	h := testHasher()
	pw := strings.Repeat("a", 72)
	hashed, err := h.Hash(pw)
	if err != nil {
		t.Fatalf("Hash(72 bytes): %v", err)
	}
	if err := h.Compare(hashed, pw); err != nil {
		t.Fatalf("Compare(72 bytes): %v", err)
	}
}

// ── CompareDecoy ──────────────────────────────────────────────────────────────
//
// Login must not reveal account existence through response time. The decoy comparison is
// what makes the "account not found" branch cost the same as a real verification.

func TestHasher_CompareDecoyPerformsWork(t *testing.T) {
	h := New(bcrypt.MinCost)
	if h.decoy == nil {
		t.Fatal("New must precompute the decoy hash; a lazy first call would itself leak timing")
	}

	// The decoy must be a real bcrypt hash at the configured cost, not a placeholder.
	if !strings.HasPrefix(string(h.decoy), "$2") {
		t.Errorf("decoy is not a bcrypt hash: %q", h.decoy)
	}
	cost, err := bcrypt.Cost(h.decoy)
	if err != nil {
		t.Fatalf("reading decoy cost: %v", err)
	}
	if cost != bcrypt.MinCost {
		t.Errorf("decoy cost = %d, want %d — a cheaper decoy would not equalise timing",
			cost, bcrypt.MinCost)
	}

	// Safe to call repeatedly.
	for i := 0; i < 3; i++ {
		h.CompareDecoy()
	}
}

// The decoy is generated at the configured cost, so raising BCRYPT_COST also raises the
// decoy's cost — otherwise the two branches would drift apart as cost is tuned.
func TestHasher_DecoyTracksConfiguredCost(t *testing.T) {
	for _, want := range []int{bcrypt.MinCost, bcrypt.MinCost + 1} {
		h := New(want)
		got, err := bcrypt.Cost(h.decoy)
		if err != nil {
			t.Fatalf("reading decoy cost: %v", err)
		}
		if got != want {
			t.Errorf("New(%d): decoy cost = %d, want %d", want, got, want)
		}
	}
}

// If decoy generation ever failed, CompareDecoy degrades to a no-op rather than panicking
// — a timing leak is bad, a crash on every failed login is worse.
func TestHasher_CompareDecoyWithoutDecoyIsNoOp(t *testing.T) {
	h := &Hasher{cost: bcrypt.MinCost} // decoy deliberately unset
	h.CompareDecoy()                   // must not panic
}
