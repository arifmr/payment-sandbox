package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
)

type Topup struct {
	ID          uuid.UUID
	MerchantID  uuid.UUID
	Amount      int64
	Status      constant.TopupStatus
	CreatedAt   time.Time
	ProcessedAt *time.Time
}
