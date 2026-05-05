package handler

import (
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

func badRequest(err error) error {
	return apperror.Wrap(apperror.KindBadRequest, "BAD_REQUEST", err.Error(), err)
}
