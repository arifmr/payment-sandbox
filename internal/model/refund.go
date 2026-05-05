package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
)

type Refund struct {
	ID              uuid.UUID
	InvoiceID       uuid.UUID
	PaymentIntentID uuid.UUID
	MerchantID      uuid.UUID
	Amount          int64
	Reason          string
	Status          constant.RefundStatus
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ProcessedAt     *time.Time
}

type RequestRefundRequest struct {
	InvoiceID string `json:"invoice_id" binding:"required,uuid"`
	Amount    int64  `json:"amount" binding:"required,gt=0"`
	Reason    string `json:"reason" binding:"max=500"`
}

type RefundActionRequest struct {
	Action string `json:"action" binding:"required,oneof=APPROVE REJECT PROCESS FAIL"`
}

type RefundResponse struct {
	ID              string     `json:"id"`
	InvoiceID       string     `json:"invoice_id"`
	PaymentIntentID string     `json:"payment_intent_id"`
	MerchantID      string     `json:"merchant_id"`
	Amount          int64      `json:"amount"`
	Reason          string     `json:"reason"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
}
