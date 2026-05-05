package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/repository"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

type PaymentService interface {
	CreateIntent(ctx context.Context, paymentToken string, method constant.PaymentMethod, payerUserID *uuid.UUID) (*model.PaymentIntent, error)
	Process(ctx context.Context, intentID uuid.UUID, success bool) (*model.PaymentIntent, error)
	GetIntent(ctx context.Context, id uuid.UUID) (*model.PaymentIntent, error)
}

type paymentService struct {
	invoices repository.InvoiceRepository
	intents  repository.PaymentIntentRepository
	uow      transaction.UnitOfWork
	strats   *strategyFactory
}

func NewPaymentService(
	inv repository.InvoiceRepository,
	pi repository.PaymentIntentRepository,
	wallets repository.WalletRepository,
	uow transaction.UnitOfWork,
) PaymentService {
	return &paymentService{
		invoices: inv,
		intents:  pi,
		uow:      uow,
		strats:   newStrategyFactory(wallets),
	}
}

func (s *paymentService) CreateIntent(ctx context.Context, paymentToken string, method constant.PaymentMethod, payerUserID *uuid.UUID) (*model.PaymentIntent, error) {
	if !method.Valid() {
		return nil, apperror.New(apperror.KindBadRequest, "INVALID_METHOD", "invalid payment method")
	}
	inv, err := s.invoices.FindByPaymentToken(ctx, paymentToken)
	if err != nil {
		return nil, err
	}
	if inv.Status == constant.InvoicePaid {
		return nil, apperror.ErrInvoiceNotPayable
	}
	if inv.Status == constant.InvoiceExpired || time.Now().After(inv.DueDate) {
		return nil, apperror.ErrInvoiceExpired
	}
	pi := &model.PaymentIntent{
		ID:          uuid.New(),
		InvoiceID:   inv.ID,
		Method:      method,
		Status:      constant.PaymentPending,
		Amount:      inv.Amount,
		PayerUserID: payerUserID,
	}
	if err := s.intents.Create(ctx, pi); err != nil {
		return nil, err
	}
	return pi, nil
}

// Process atomically transitions the intent to SUCCESS or FAILED and applies the side effects.
// On SUCCESS: invoice -> PAID and wallet credits/debits via the strategy.
func (s *paymentService) Process(ctx context.Context, intentID uuid.UUID, success bool) (*model.PaymentIntent, error) {
	var out *model.PaymentIntent
	err := s.uow.Do(ctx, func(ctx context.Context) error {
		intent, err := s.intents.FindByID(ctx, intentID)
		if err != nil {
			return err
		}
		if intent.Status != constant.PaymentPending {
			return apperror.ErrInvalidState
		}

		now := time.Now().UTC()
		newStatus := constant.PaymentFailed
		if success {
			newStatus = constant.PaymentSuccess
		}

		if err := s.intents.UpdateStatus(ctx, intentID, constant.PaymentPending, newStatus, &now); err != nil {
			return err
		}

		if success {
			inv, err := s.invoices.FindByID(ctx, intent.InvoiceID)
			if err != nil {
				return err
			}
			if inv.Status != constant.InvoicePending {
				return apperror.ErrInvoiceNotPayable
			}
			if err := s.invoices.UpdateStatus(ctx, inv.ID, constant.InvoicePending, constant.InvoicePaid, &now); err != nil {
				return err
			}
			strat, err := s.strats.For(intent.Method)
			if err != nil {
				return err
			}
			if err := strat.OnSuccess(ctx, intent, inv); err != nil {
				return err
			}
		}

		intent.Status = newStatus
		intent.ProcessedAt = &now
		out = intent
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *paymentService) GetIntent(ctx context.Context, id uuid.UUID) (*model.PaymentIntent, error) {
	return s.intents.FindByID(ctx, id)
}
