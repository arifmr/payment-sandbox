package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/pkg/token"
	"github.com/dboarif/payment-sandbox/internal/repository"
)

type CreateInvoiceInput struct {
	MerchantID    uuid.UUID
	CustomerName  string
	CustomerEmail string
	Description   string
	Amount        int64
	DueDate       time.Time
}

type InvoiceService interface {
	Create(ctx context.Context, in CreateInvoiceInput) (*model.Invoice, error)
	GetByID(ctx context.Context, id uuid.UUID, merchantID uuid.UUID) (*model.Invoice, error)
	GetByPaymentToken(ctx context.Context, paymentToken string) (*model.Invoice, error)
	List(ctx context.Context, f repository.InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error)
	ExpireDue(ctx context.Context) (int64, error)
}

type invoiceService struct {
	invoices repository.InvoiceRepository
}

func NewInvoiceService(i repository.InvoiceRepository) InvoiceService {
	return &invoiceService{invoices: i}
}

func (s *invoiceService) Create(ctx context.Context, in CreateInvoiceInput) (*model.Invoice, error) {
	if in.Amount <= 0 {
		return nil, apperror.ErrInvalidAmount
	}
	if in.DueDate.Before(time.Now()) {
		return nil, apperror.New(apperror.KindBadRequest, "INVALID_DUE_DATE", "due_date must be in the future")
	}
	num, err := token.InvoiceNumber()
	if err != nil {
		return nil, err
	}
	tok, err := token.Random(32)
	if err != nil {
		return nil, err
	}
	inv := &model.Invoice{
		ID:            uuid.New(),
		InvoiceNumber: num,
		MerchantID:    in.MerchantID,
		CustomerName:  in.CustomerName,
		CustomerEmail: in.CustomerEmail,
		Description:   in.Description,
		Amount:        in.Amount,
		Status:        constant.InvoicePending,
		DueDate:       in.DueDate,
		PaymentToken:  tok,
	}
	if err := s.invoices.Create(ctx, inv); err != nil {
		return nil, err
	}
	return inv, nil
}

func (s *invoiceService) GetByID(ctx context.Context, id uuid.UUID, merchantID uuid.UUID) (*model.Invoice, error) {
	inv, err := s.invoices.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if inv.MerchantID != merchantID {
		return nil, apperror.ErrForbidden
	}
	return inv, nil
}

func (s *invoiceService) GetByPaymentToken(ctx context.Context, paymentToken string) (*model.Invoice, error) {
	return s.invoices.FindByPaymentToken(ctx, paymentToken)
}

func (s *invoiceService) List(ctx context.Context, f repository.InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error) {
	return s.invoices.List(ctx, f, offset, limit)
}

func (s *invoiceService) ExpireDue(ctx context.Context) (int64, error) {
	return s.invoices.MarkExpired(ctx, time.Now().UTC())
}
