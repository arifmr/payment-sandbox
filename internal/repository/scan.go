package repository

import (
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

// itoa is a tiny alias used to inline placeholder indices in dynamically built
// SQL statements (e.g. "$"+itoa(n)). strconv.Itoa is fine but verbose at every
// call site.
func itoa(n int) string { return strconv.Itoa(n) }

// nullTimePtr converts sql.NullTime to *time.Time.
func nullTimePtr(n sql.NullTime) *time.Time {
	if n.Valid {
		t := n.Time
		return &t
	}
	return nil
}

// nullStringToUUIDPtr parses a NullString into *uuid.UUID; invalid uuids return nil.
func nullStringToUUIDPtr(n sql.NullString) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id, err := uuid.Parse(n.String)
	if err != nil {
		return nil
	}
	return &id
}

// uuidPtrToNullString prepares a *uuid.UUID for an INSERT/UPDATE binding.
func uuidPtrToNullString(p *uuid.UUID) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: p.String(), Valid: true}
}

// timePtrToNullTime prepares a *time.Time for an INSERT/UPDATE binding.
func timePtrToNullTime(p *time.Time) sql.NullTime {
	if p == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *p, Valid: true}
}

// mapNoRowsToNotFound converts sql.ErrNoRows to apperror.ErrNotFound; other errors pass through.
func mapNoRowsToNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperror.ErrNotFound
	}
	return err
}
