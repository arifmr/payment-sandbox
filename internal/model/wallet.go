package model

import (
	"time"

	"github.com/google/uuid"
)

type Wallet struct {
	ID         uuid.UUID
	MerchantID uuid.UUID
	Balance    int64
	Version    int64
	UpdatedAt  time.Time
}

type WalletResponse struct {
	MerchantID string    `json:"merchant_id"`
	Balance    int64     `json:"balance"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type TopupRequest struct {
	Amount int64 `json:"amount" binding:"required,gt=0"`
}

type TopupActionRequest struct {
	Action string `json:"action" binding:"required,oneof=SUCCESS FAILED"`
}

type TopupResponse struct {
	ID          string     `json:"id"`
	MerchantID  string     `json:"merchant_id"`
	Amount      int64      `json:"amount"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}
