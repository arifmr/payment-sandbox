package apperror

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestHTTPStatus_KindMapping(t *testing.T) {
	cases := []struct {
		kind Kind
		want int
	}{
		{KindBadRequest, http.StatusBadRequest},
		{KindUnauthorized, http.StatusUnauthorized},
		{KindForbidden, http.StatusForbidden},
		{KindNotFound, http.StatusNotFound},
		{KindConflict, http.StatusConflict},
		{KindInvalidState, http.StatusUnprocessableEntity},
		{KindUnprocessable, http.StatusUnprocessableEntity},
		{KindInternal, http.StatusInternalServerError},
		{Kind("SOMETHING_NEW"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		got := HTTPStatus(New(tc.kind, "CODE", "message"))
		if got != tc.want {
			t.Errorf("HTTPStatus(%s) = %d, want %d", tc.kind, got, tc.want)
		}
	}
}

// A plain error carries no kind, so it must be treated as an internal failure
// rather than leaking through as a 200 or a client error.
func TestHTTPStatus_PlainErrorIsInternal(t *testing.T) {
	if got := HTTPStatus(errors.New("boom")); got != http.StatusInternalServerError {
		t.Errorf("HTTPStatus(plain error) = %d, want 500", got)
	}
	if got := HTTPStatus(nil); got != http.StatusInternalServerError {
		t.Errorf("HTTPStatus(nil) = %d, want 500", got)
	}
}

// The HTTP layer sees errors after they have travelled up through wrapping, so
// the kind must survive fmt.Errorf("%w").
func TestHTTPStatus_ThroughWrappedError(t *testing.T) {
	wrapped := fmt.Errorf("service failed: %w", ErrNotFound)
	if got := HTTPStatus(wrapped); got != http.StatusNotFound {
		t.Errorf("HTTPStatus(wrapped ErrNotFound) = %d, want 404", got)
	}
}

func TestError_MessageFormat(t *testing.T) {
	bare := New(KindBadRequest, "INVALID_AMOUNT", "amount must be positive")
	if got, want := bare.Error(), "INVALID_AMOUNT: amount must be positive"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	cause := errors.New("underlying")
	wrapped := Wrap(KindInternal, "DB", "query failed", cause)
	if got, want := wrapped.Error(), "DB: query failed: underlying"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestError_UnwrapExposesCause(t *testing.T) {
	cause := errors.New("root cause")
	err := Wrap(KindInternal, "DB", "query failed", cause)

	if !errors.Is(err, cause) {
		t.Error("errors.Is must find the wrapped cause")
	}
	if errors.Unwrap(err) != cause {
		t.Error("Unwrap must return the original cause")
	}
	if errors.Unwrap(New(KindInternal, "X", "y")) != nil {
		t.Error("Unwrap on an error without a cause must return nil")
	}
}

func TestIsKind(t *testing.T) {
	if !IsKind(ErrEmailTaken, KindConflict) {
		t.Error("ErrEmailTaken should report kind CONFLICT")
	}
	if IsKind(ErrEmailTaken, KindNotFound) {
		t.Error("ErrEmailTaken must not report kind NOT_FOUND")
	}
	if !IsKind(fmt.Errorf("wrapped: %w", ErrInvalidState), KindInvalidState) {
		t.Error("IsKind must see through wrapping")
	}
	if IsKind(errors.New("plain"), KindInternal) {
		t.Error("a plain error has no kind")
	}
	if IsKind(nil, KindInternal) {
		t.Error("nil has no kind")
	}
}

// Sentinels are compared with errors.Is across layers, so each must be a distinct
// value with the status the API contract documents.
func TestSentinels_KindsAndIdentity(t *testing.T) {
	cases := []struct {
		err  *Error
		code string
		want int
	}{
		{ErrInvalidCredentials, "INVALID_CREDENTIALS", http.StatusUnauthorized},
		{ErrEmailTaken, "EMAIL_TAKEN", http.StatusConflict},
		{ErrUnauthorized, "UNAUTHORIZED", http.StatusUnauthorized},
		{ErrForbidden, "FORBIDDEN", http.StatusForbidden},
		{ErrNotFound, "NOT_FOUND", http.StatusNotFound},
		{ErrInvalidState, "INVALID_STATE", http.StatusUnprocessableEntity},
		{ErrInsufficientFunds, "INSUFFICIENT_FUNDS", http.StatusUnprocessableEntity},
		{ErrInvalidAmount, "INVALID_AMOUNT", http.StatusBadRequest},
		{ErrInvoiceExpired, "INVOICE_EXPIRED", http.StatusUnprocessableEntity},
		{ErrInvoiceNotPayable, "INVOICE_NOT_PAYABLE", http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		if tc.err.Code != tc.code {
			t.Errorf("code = %q, want %q", tc.err.Code, tc.code)
		}
		if got := HTTPStatus(tc.err); got != tc.want {
			t.Errorf("%s: HTTPStatus = %d, want %d", tc.code, got, tc.want)
		}
	}

	// Distinct sentinels must not satisfy errors.Is for one another.
	if errors.Is(ErrNotFound, ErrForbidden) {
		t.Error("ErrNotFound and ErrForbidden must be distinct")
	}
}
