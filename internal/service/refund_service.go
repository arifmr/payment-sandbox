package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/repository"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

// RefundAction is the admin action enum for the single refund endpoint.
type RefundAction string

const (
	RefundActionApprove RefundAction = "APPROVE"
	RefundActionReject  RefundAction = "REJECT"
	RefundActionProcess RefundAction = "PROCESS" // mark SUCCESS
	RefundActionFail    RefundAction = "FAIL"    // mark FAILED
)

func (a RefundAction) Valid() bool {
	return a.target() != ""
}

// target maps an admin action to the refund status it drives to. An unknown
// action maps to the empty status, which the FSM will always reject.
func (a RefundAction) target() constant.RefundStatus {
	switch a {
	case RefundActionApprove:
		return constant.RefundApproved
	case RefundActionReject:
		return constant.RefundRejected
	case RefundActionProcess:
		return constant.RefundSuccess
	case RefundActionFail:
		return constant.RefundFailed
	}
	return ""
}

type RefundService interface {
	Request(ctx context.Context, merchantID, invoiceID uuid.UUID, amount int64, reason string) (*model.Refund, error)
	AdminAction(ctx context.Context, refundID uuid.UUID, action RefundAction) (*model.Refund, error)
	ListByMerchant(ctx context.Context, merchantID uuid.UUID, offset, limit int) ([]model.Refund, int64, error)
	List(ctx context.Context, offset, limit int) ([]model.Refund, int64, error)
	GetByID(ctx context.Context, id uuid.UUID) (*model.Refund, error)
}

type refundService struct {
	refunds  repository.RefundRepository
	invoices repository.InvoiceRepository
	intents  repository.PaymentIntentRepository
	wallets  repository.WalletRepository
	uow      transaction.UnitOfWork
}

func NewRefundService(
	r repository.RefundRepository,
	inv repository.InvoiceRepository,
	pi repository.PaymentIntentRepository,
	w repository.WalletRepository,
	uow transaction.UnitOfWork,
) RefundService {
	return &refundService{refunds: r, invoices: inv, intents: pi, wallets: w, uow: uow}
}

// Request opens a refund for a PAID invoice. The whole check-then-insert runs in one
// transaction with the invoice row locked, so concurrent requests cannot each observe
// the same remaining balance and collectively over-refund the invoice.
func (s *refundService) Request(ctx context.Context, merchantID, invoiceID uuid.UUID, amount int64, reason string) (*model.Refund, error) {
	if amount <= 0 {
		return nil, apperror.ErrInvalidAmount
	}

	var out *model.Refund
	err := s.uow.Do(ctx, func(ctx context.Context) error {
		inv, err := s.invoices.FindByIDForUpdate(ctx, invoiceID)
		if err != nil {
			return err
		}
		if inv.MerchantID != merchantID {
			return apperror.ErrForbidden
		}
		if inv.Status != constant.InvoicePaid {
			return apperror.New(apperror.KindUnprocessable, "INVOICE_NOT_PAID", "only PAID invoices can be refunded")
		}

		// Cap the total across every refund still holding a claim on this invoice,
		// not just this single request.
		outstanding, err := s.refunds.SumOutstandingByInvoice(ctx, inv.ID)
		if err != nil {
			return err
		}
		if outstanding+amount > inv.Amount {
			return apperror.New(apperror.KindUnprocessable, "REFUND_EXCEEDS_INVOICE",
				"refund amount exceeds the invoice's remaining refundable balance")
		}

		pi, err := s.intents.FindLatestSuccessByInvoice(ctx, inv.ID)
		if err != nil {
			if errors.Is(err, apperror.ErrNotFound) {
				return apperror.New(apperror.KindUnprocessable, "NO_SUCCESSFUL_PAYMENT",
					"invoice has no successful payment to refund")
			}
			return err
		}
		rf := &model.Refund{
			ID:              uuid.New(),
			InvoiceID:       inv.ID,
			PaymentIntentID: pi.ID,
			MerchantID:      merchantID,
			Amount:          amount,
			Reason:          reason,
			Status:          constant.RefundRequested,
		}
		if err := s.refunds.Create(ctx, rf); err != nil {
			return err
		}
		out = rf
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AdminAction routes a single admin action through the refund state machine.
//
//	APPROVE: REQUESTED -> APPROVED
//	REJECT:  REQUESTED -> REJECTED  (terminal)
//	PROCESS: APPROVED  -> SUCCESS   (debit merchant wallet, atomic)
//	FAIL:    APPROVED  -> FAILED    (terminal)
func (s *refundService) AdminAction(ctx context.Context, refundID uuid.UUID, action RefundAction) (*model.Refund, error) {
	if !action.Valid() {
		return nil, apperror.New(apperror.KindBadRequest, "INVALID_ACTION", "unknown refund action")
	}

	var out *model.Refund
	err := s.uow.Do(ctx, func(ctx context.Context) error {
		rf, err := s.refunds.FindByID(ctx, refundID)
		if err != nil {
			return err
		}
		now := time.Now().UTC()

		target := action.target()
		if !constant.RefundFSM.Can(rf.Status, target) {
			return apperror.ErrInvalidState
		}

		// APPROVE is the only non-terminal transition, so it leaves processed_at unset.
		var processedAt *time.Time
		if target != constant.RefundApproved {
			processedAt = &now
		}

		if err := s.refunds.UpdateStatus(ctx, refundID, rf.Status, target, processedAt); err != nil {
			return err
		}
		// SUCCESS is the only action that moves money.
		if target == constant.RefundSuccess {
			if err := s.wallets.AddBalance(ctx, rf.MerchantID, -rf.Amount); err != nil {
				return err
			}
		}

		rf.Status = target
		rf.ProcessedAt = processedAt
		out = rf
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *refundService) ListByMerchant(ctx context.Context, merchantID uuid.UUID, offset, limit int) ([]model.Refund, int64, error) {
	return s.refunds.ListByMerchant(ctx, merchantID, offset, limit)
}

func (s *refundService) List(ctx context.Context, offset, limit int) ([]model.Refund, int64, error) {
	return s.refunds.List(ctx, offset, limit)
}

func (s *refundService) GetByID(ctx context.Context, id uuid.UUID) (*model.Refund, error) {
	return s.refunds.FindByID(ctx, id)
}
