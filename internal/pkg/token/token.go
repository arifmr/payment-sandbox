package token

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Random returns a cryptographically random hex string of length 2*nBytes.
func Random(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// InvoiceNumber generates a human-readable invoice number.
// Example: INV-20260429-1A2B3C4D5E
//
// 5 random bytes (10 hex chars) give ~1.1e12 values per day, so a same-day
// collision is vanishingly unlikely — but the DB unique constraint remains the
// authority and callers retry on violation.
func InvoiceNumber() (string, error) {
	suffix, err := Random(5) // 10 hex chars
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("INV-%s-%s", time.Now().UTC().Format("20060102"), strings.ToUpper(suffix)), nil
}
