package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
)

type PaymentIntent struct {
	ID          uuid.UUID
	InvoiceID   uuid.UUID
	Method      constant.PaymentMethod
	Status      constant.PaymentIntentStatus
	Amount      int64
	PayerUserID *uuid.UUID
	CreatedAt   time.Time
	ProcessedAt *time.Time
}

type PublicInvoiceResponse struct {
	InvoiceNumber string    `json:"invoice_number"`
	MerchantName  string    `json:"merchant_name"`
	CustomerName  string    `json:"customer_name"`
	Description   string    `json:"description"`
	Amount        int64     `json:"amount"`
	Status        string    `json:"status"`
	DueDate       time.Time `json:"due_date"`
}

type CreatePaymentRequest struct {
	Method string `json:"method" binding:"required,oneof=WALLET VA_DUMMY EWALLET_DUMMY"`
}

type PaymentIntentResponse struct {
	ID          string     `json:"id"`
	InvoiceID   string     `json:"invoice_id"`
	Method      string     `json:"method"`
	Status      string     `json:"status"`
	Amount      int64      `json:"amount"`
	CreatedAt   time.Time  `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

type PaymentActionRequest struct {
	Action string `json:"action" binding:"required,oneof=SUCCESS FAILED"`
}
