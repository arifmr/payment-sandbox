package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/repository"
)

// PaymentStrategy executes the side effects of a SUCCESS payment for a particular method.
// It runs inside the same transaction as the status updates.
type PaymentStrategy interface {
	OnSuccess(ctx context.Context, intent *model.PaymentIntent, invoice *model.Invoice) error
}

// walletStrategy: payer (logged-in user with role MERCHANT acting as payer) is debited,
// and the invoice merchant is credited. If no payer is attached we skip debit (sandbox).
type walletStrategy struct{ wallets repository.WalletRepository }

func (w *walletStrategy) OnSuccess(ctx context.Context, intent *model.PaymentIntent, invoice *model.Invoice) error {
	// Credit merchant always.
	if err := w.wallets.AddBalance(ctx, invoice.MerchantID, intent.Amount); err != nil {
		return err
	}
	// Debit payer wallet if a payer is identified.
	if intent.PayerUserID != nil && *intent.PayerUserID != uuid.Nil {
		if err := w.wallets.AddBalance(ctx, *intent.PayerUserID, -intent.Amount); err != nil {
			return err
		}
	}
	return nil
}

// dummyStrategy: VA / E-Wallet are external in real life — here we just credit merchant.
type dummyStrategy struct{ wallets repository.WalletRepository }

func (d *dummyStrategy) OnSuccess(ctx context.Context, intent *model.PaymentIntent, invoice *model.Invoice) error {
	return d.wallets.AddBalance(ctx, invoice.MerchantID, intent.Amount)
}

// strategyFactory selects a strategy based on payment method. Returns ErrInvalidState for unknown methods.
type strategyFactory struct {
	wallet *walletStrategy
	dummy  *dummyStrategy
}

func newStrategyFactory(wallets repository.WalletRepository) *strategyFactory {
	return &strategyFactory{
		wallet: &walletStrategy{wallets: wallets},
		dummy:  &dummyStrategy{wallets: wallets},
	}
}

func (f *strategyFactory) For(method constant.PaymentMethod) (PaymentStrategy, error) {
	switch method {
	case constant.PaymentMethodWallet:
		return f.wallet, nil
	case constant.PaymentMethodVADummy, constant.PaymentMethodEwalletDummy:
		return f.dummy, nil
	default:
		return nil, apperror.New(apperror.KindBadRequest, "UNSUPPORTED_METHOD", "unsupported payment method")
	}
}
