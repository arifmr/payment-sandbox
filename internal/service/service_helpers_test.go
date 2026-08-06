package service

import (
	"errors"

	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

// errorCode extracts the domain error code so tests can assert on the exact
// contract the API exposes rather than on message text.
func errorCode(err error) string {
	var ae *apperror.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}
