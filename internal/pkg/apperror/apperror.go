package apperror

import (
	"errors"
	"fmt"
	"net/http"
)

type Kind string

const (
	KindBadRequest    Kind = "BAD_REQUEST"
	KindUnauthorized  Kind = "UNAUTHORIZED"
	KindForbidden     Kind = "FORBIDDEN"
	KindNotFound      Kind = "NOT_FOUND"
	KindConflict      Kind = "CONFLICT"
	KindInvalidState  Kind = "INVALID_STATE"
	KindUnprocessable Kind = "UNPROCESSABLE"
	KindInternal      Kind = "INTERNAL"
)

type Error struct {
	Kind    Kind
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

func Wrap(kind Kind, code, message string, err error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Err: err}
}

func HTTPStatus(err error) int {
	var ae *Error
	if !errors.As(err, &ae) {
		return http.StatusInternalServerError
	}
	switch ae.Kind {
	case KindBadRequest:
		return http.StatusBadRequest
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindInvalidState, KindUnprocessable:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

// Common sentinels (use New() to keep stack-friendly).
var (
	ErrInvalidCredentials = New(KindUnauthorized, "INVALID_CREDENTIALS", "email or password is incorrect")
	ErrEmailTaken         = New(KindConflict, "EMAIL_TAKEN", "email already registered")
	ErrUnauthorized       = New(KindUnauthorized, "UNAUTHORIZED", "missing or invalid token")
	ErrForbidden          = New(KindForbidden, "FORBIDDEN", "insufficient permissions")
	ErrNotFound           = New(KindNotFound, "NOT_FOUND", "resource not found")
	ErrInvalidState       = New(KindInvalidState, "INVALID_STATE", "invalid state transition")
	ErrInsufficientFunds  = New(KindUnprocessable, "INSUFFICIENT_FUNDS", "wallet balance is insufficient")
	ErrInvalidAmount      = New(KindBadRequest, "INVALID_AMOUNT", "amount must be greater than zero")
	ErrInvoiceExpired     = New(KindUnprocessable, "INVOICE_EXPIRED", "invoice has expired")
	ErrInvoiceNotPayable  = New(KindUnprocessable, "INVOICE_NOT_PAYABLE", "invoice is not payable in current state")
)
