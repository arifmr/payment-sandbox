package repository

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

// Postgres error codes we care about.
const pgUniqueViolation = "23505"

// isUniqueViolation reports whether err is a Postgres unique-constraint failure.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// mapWriteError turns a driver-level constraint failure into a domain error so the
// HTTP layer answers 409 instead of 500, and so services can retry generated
// values (invoice_number, payment_token) without importing the driver.
func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return apperror.Wrap(apperror.KindConflict, "DUPLICATE", "resource already exists", err)
	}
	return err
}
