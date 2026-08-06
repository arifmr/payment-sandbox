package jwt

import (
	"strings"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "test-secret-that-is-long-enough-32b"

func TestManager_IssueAndParse(t *testing.T) {
	m := New(testSecret, 15*time.Minute)
	userID := uuid.NewString()

	tok, exp, err := m.Issue(userID, "MERCHANT")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if tok == "" {
		t.Fatal("Issue returned an empty token")
	}
	if time.Until(exp) <= 0 || time.Until(exp) > 15*time.Minute+time.Second {
		t.Fatalf("expiry %v is not ~15m in the future", exp)
	}

	claims, err := m.Parse(tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("UserID = %q, want %q", claims.UserID, userID)
	}
	if claims.Role != "MERCHANT" {
		t.Errorf("Role = %q, want MERCHANT", claims.Role)
	}
	if claims.ExpiresAt == nil {
		t.Fatal("token must carry an exp claim (SRS §3.3)")
	}
	if claims.ID == "" {
		t.Error("token should carry a jti")
	}
}

// SRS §3.3: the JWT must expire, and an expired one must not be accepted.
func TestManager_Parse_RejectsExpiredToken(t *testing.T) {
	m := New(testSecret, -1*time.Minute) // already expired at issue time
	tok, _, err := m.Issue(uuid.NewString(), "MERCHANT")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := m.Parse(tok); err == nil {
		t.Fatal("Parse accepted an expired token")
	}
}

func TestManager_Parse_RejectsWrongSecret(t *testing.T) {
	issuer := New(testSecret, time.Hour)
	tok, _, err := issuer.Issue(uuid.NewString(), "ADMIN")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	attacker := New("a-completely-different-secret-key!", time.Hour)
	if _, err := attacker.Parse(tok); err == nil {
		t.Fatal("Parse accepted a token signed with a different secret")
	}
}

func TestManager_Parse_RejectsGarbage(t *testing.T) {
	m := New(testSecret, time.Hour)
	for _, tok := range []string{"", "not-a-jwt", "a.b.c", strings.Repeat("x", 64)} {
		if _, err := m.Parse(tok); err == nil {
			t.Errorf("Parse(%q) accepted a malformed token", tok)
		}
	}
}

// Guards against the classic "alg: none" / algorithm-confusion downgrade.
func TestManager_Parse_RejectsUnsignedToken(t *testing.T) {
	m := New(testSecret, time.Hour)

	unsigned := gojwt.NewWithClaims(gojwt.SigningMethodNone, &Claims{
		UserID: uuid.NewString(),
		Role:   "ADMIN",
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	raw, err := unsigned.SignedString(gojwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building unsigned token: %v", err)
	}

	if _, err := m.Parse(raw); err == nil {
		t.Fatal("Parse accepted an unsigned (alg=none) token")
	}
}

func TestManager_Issue_TokensAreDistinct(t *testing.T) {
	m := New(testSecret, time.Hour)
	userID := uuid.NewString()

	first, _, err := m.Issue(userID, "MERCHANT")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second, _, err := m.Issue(userID, "MERCHANT")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Distinct jti keeps two same-second logins from producing identical tokens.
	if first == second {
		t.Error("two issues for the same user produced an identical token")
	}
}
