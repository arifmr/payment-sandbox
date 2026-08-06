package token

import (
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRandom_LengthAndEncoding(t *testing.T) {
	for _, n := range []int{1, 3, 16, 32} {
		got, err := Random(n)
		if err != nil {
			t.Fatalf("Random(%d): %v", n, err)
		}
		if len(got) != 2*n {
			t.Errorf("Random(%d) length = %d, want %d", n, len(got), 2*n)
		}
		if _, err := hex.DecodeString(got); err != nil {
			t.Errorf("Random(%d) = %q is not valid hex: %v", n, got, err)
		}
	}
}

func TestRandom_ZeroBytes(t *testing.T) {
	got, err := Random(0)
	if err != nil {
		t.Fatalf("Random(0): %v", err)
	}
	if got != "" {
		t.Errorf("Random(0) = %q, want empty string", got)
	}
}

// SRS §3.3: the payment token must be random and unique. A birthday-style check
// over many draws is a cheap regression guard against a non-random source.
func TestRandom_NoCollisionsAcrossManyDraws(t *testing.T) {
	const draws = 2000
	seen := make(map[string]struct{}, draws)
	for i := 0; i < draws; i++ {
		got, err := Random(32)
		if err != nil {
			t.Fatalf("Random: %v", err)
		}
		if _, dup := seen[got]; dup {
			t.Fatalf("duplicate 32-byte token after %d draws", i)
		}
		seen[got] = struct{}{}
	}
}

var invoiceNumberPattern = regexp.MustCompile(`^INV-\d{8}-[0-9A-F]{10}$`)

func TestInvoiceNumber_Format(t *testing.T) {
	got, err := InvoiceNumber()
	if err != nil {
		t.Fatalf("InvoiceNumber: %v", err)
	}
	if !invoiceNumberPattern.MatchString(got) {
		t.Fatalf("InvoiceNumber() = %q, want INV-YYYYMMDD-XXXXXXXXXX", got)
	}
	if want := time.Now().UTC().Format("20060102"); !strings.Contains(got, want) {
		t.Errorf("InvoiceNumber() = %q, expected today's UTC date %s", got, want)
	}
	if strings.ToUpper(got) != got {
		t.Errorf("InvoiceNumber() = %q, suffix should be upper-case hex", got)
	}
}

func TestInvoiceNumber_Unique(t *testing.T) {
	const draws = 1000
	seen := make(map[string]struct{}, draws)
	for i := 0; i < draws; i++ {
		got, err := InvoiceNumber()
		if err != nil {
			t.Fatalf("InvoiceNumber: %v", err)
		}
		if _, dup := seen[got]; dup {
			t.Fatalf("duplicate invoice number %q after %d draws", got, i)
		}
		seen[got] = struct{}{}
	}
}
