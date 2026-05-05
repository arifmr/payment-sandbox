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

// RefundAction is the admin action enum for the single refund endpoint.
type RefundAction string

const (
	RefundActionApprove RefundAction = "APPROVE"
	RefundActionReject  RefundAction = "REJECT"
	RefundActionProcess RefundAction = "PROCESS" // mark SUCCESS
	RefundActionFail    RefundAction = "FAIL"    // mark FAILED
)

func (a RefundAction) Valid() bool {
	switch a {
	case RefundActionApprove, RefundActionReject, RefundActionProcess, RefundActionFail:
		return true
	}
	return false
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

func (s *refundService) Request(ctx context.Context, merchantID, invoiceID uuid.UUID, amount int64, reason string) (*model.Refund, error) {
	if amount <= 0 {
		return nil, apperror.ErrInvalidAmount
	}
	inv, err := s.invoices.FindByID(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	if inv.MerchantID != merchantID {
		return nil, apperror.ErrForbidden
	}
	if inv.Status != constant.InvoicePaid {
		return nil, apperror.New(apperror.KindUnprocessable, "INVOICE_NOT_PAID", "only PAID invoices can be refunded")
	}
	if amount > inv.Amount {
		return nil, apperror.New(apperror.KindBadRequest, "REFUND_EXCEEDS_INVOICE", "refund amount exceeds invoice amount")
	}
	pi, err := s.intents.FindLatestSuccessByInvoice(ctx, inv.ID)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	return rf, nil
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

		switch action {
		case RefundActionApprove:
			if rf.Status != constant.RefundRequested {
				return apperror.ErrInvalidState
			}
			if err := s.refunds.UpdateStatus(ctx, refundID, constant.RefundRequested, constant.RefundApproved, nil); err != nil {
				return err
			}
			rf.Status = constant.RefundApproved
		case RefundActionReject:
			if rf.Status != constant.RefundRequested {
				return apperror.ErrInvalidState
			}
			if err := s.refunds.UpdateStatus(ctx, refundID, constant.RefundRequested, constant.RefundRejected, &now); err != nil {
				return err
			}
			rf.Status = constant.RefundRejected
			rf.ProcessedAt = &now
		case RefundActionProcess:
			if rf.Status != constant.RefundApproved {
				return apperror.ErrInvalidState
			}
			if err := s.refunds.UpdateStatus(ctx, refundID, constant.RefundApproved, constant.RefundSuccess, &now); err != nil {
				return err
			}
			if err := s.wallets.AddBalance(ctx, rf.MerchantID, -rf.Amount); err != nil {
				return err
			}
			rf.Status = constant.RefundSuccess
			rf.ProcessedAt = &now
		case RefundActionFail:
			if rf.Status != constant.RefundApproved {
				return apperror.ErrInvalidState
			}
			if err := s.refunds.UpdateStatus(ctx, refundID, constant.RefundApproved, constant.RefundFailed, &now); err != nil {
				return err
			}
			rf.Status = constant.RefundFailed
			rf.ProcessedAt = &now
		}
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
