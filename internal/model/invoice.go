package model

import (
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
)

type Invoice struct {
	ID            uuid.UUID
	InvoiceNumber string
	MerchantID    uuid.UUID
	CustomerName  string
	CustomerEmail string
	Description   string
	Amount        int64
	Status        constant.InvoiceStatus
	DueDate       time.Time
	PaymentToken  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	PaidAt        *time.Time
}

type CreateInvoiceRequest struct {
	CustomerName  string    `json:"customer_name" binding:"required,min=1,max=255"`
	CustomerEmail string    `json:"customer_email" binding:"omitempty,email,max=255"`
	Description   string    `json:"description" binding:"max=500"`
	Amount        int64     `json:"amount" binding:"required,gt=0"`
	DueDate       time.Time `json:"due_date" binding:"required"`
}

type InvoiceResponse struct {
	ID            string     `json:"id"`
	InvoiceNumber string     `json:"invoice_number"`
	MerchantID    string     `json:"merchant_id"`
	CustomerName  string     `json:"customer_name"`
	CustomerEmail string     `json:"customer_email"`
	Description   string     `json:"description"`
	Amount        int64      `json:"amount"`
	Status        string     `json:"status"`
	DueDate       time.Time  `json:"due_date"`
	PaymentToken  string     `json:"payment_token"`
	PaymentLink   string     `json:"payment_link,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
}
