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

type WalletService interface {
	GetBalance(ctx context.Context, merchantID uuid.UUID) (*model.Wallet, error)
	RequestTopup(ctx context.Context, merchantID uuid.UUID, amount int64) (*model.Topup, error)
	ProcessTopup(ctx context.Context, topupID uuid.UUID, success bool) (*model.Topup, error)
	ListTopups(ctx context.Context, merchantID *uuid.UUID, offset, limit int) ([]model.Topup, int64, error)
}

type walletService struct {
	topups  repository.TopupRepository
	wallets repository.WalletRepository
	uow     transaction.UnitOfWork
}

func NewWalletService(t repository.TopupRepository, w repository.WalletRepository, uow transaction.UnitOfWork) WalletService {
	return &walletService{topups: t, wallets: w, uow: uow}
}

func (s *walletService) GetBalance(ctx context.Context, merchantID uuid.UUID) (*model.Wallet, error) {
	return s.wallets.FindByMerchantID(ctx, merchantID)
}

func (s *walletService) RequestTopup(ctx context.Context, merchantID uuid.UUID, amount int64) (*model.Topup, error) {
	if amount <= 0 {
		return nil, apperror.ErrInvalidAmount
	}
	t := &model.Topup{
		ID:         uuid.New(),
		MerchantID: merchantID,
		Amount:     amount,
		Status:     constant.TopupPending,
	}
	if err := s.topups.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// ProcessTopup atomically finalizes a topup: PENDING -> SUCCESS/FAILED, and on SUCCESS credits the wallet.
func (s *walletService) ProcessTopup(ctx context.Context, topupID uuid.UUID, success bool) (*model.Topup, error) {
	var out *model.Topup
	err := s.uow.Do(ctx, func(ctx context.Context) error {
		t, err := s.topups.FindByID(ctx, topupID)
		if err != nil {
			return err
		}
		if t.Status != constant.TopupPending {
			return apperror.ErrInvalidState
		}
		now := time.Now().UTC()
		newStatus := constant.TopupFailed
		if success {
			newStatus = constant.TopupSuccess
		}
		if err := s.topups.UpdateStatus(ctx, topupID, newStatus, &now); err != nil {
			return err
		}
		if success {
			if err := s.wallets.AddBalance(ctx, t.MerchantID, t.Amount); err != nil {
				return err
			}
		}
		t.Status = newStatus
		t.ProcessedAt = &now
		out = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *walletService) ListTopups(ctx context.Context, merchantID *uuid.UUID, offset, limit int) ([]model.Topup, int64, error) {
	return s.topups.List(ctx, merchantID, offset, limit)
}
