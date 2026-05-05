package model

func ToUser(u *User) UserResponse {
	return UserResponse{
		ID:    u.ID.String(),
		Email: u.Email,
		Name:  u.Name,
		Role:  string(u.Role),
	}
}

func ToWallet(w *Wallet) WalletResponse {
	return WalletResponse{
		MerchantID: w.MerchantID.String(),
		Balance:    w.Balance,
		UpdatedAt:  w.UpdatedAt,
	}
}

func ToTopup(t *Topup) TopupResponse {
	return TopupResponse{
		ID:          t.ID.String(),
		MerchantID:  t.MerchantID.String(),
		Amount:      t.Amount,
		Status:      string(t.Status),
		CreatedAt:   t.CreatedAt,
		ProcessedAt: t.ProcessedAt,
	}
}

func ToInvoice(inv *Invoice, paymentLinkBase string) InvoiceResponse {
	resp := InvoiceResponse{
		ID:            inv.ID.String(),
		InvoiceNumber: inv.InvoiceNumber,
		MerchantID:    inv.MerchantID.String(),
		CustomerName:  inv.CustomerName,
		CustomerEmail: inv.CustomerEmail,
		Description:   inv.Description,
		Amount:        inv.Amount,
		Status:        string(inv.Status),
		DueDate:       inv.DueDate,
		PaymentToken:  inv.PaymentToken,
		CreatedAt:     inv.CreatedAt,
		PaidAt:        inv.PaidAt,
	}
	if paymentLinkBase != "" {
		resp.PaymentLink = paymentLinkBase + "/" + inv.PaymentToken
	}
	return resp
}

func ToPublicInvoice(inv *Invoice, merchantName string) PublicInvoiceResponse {
	return PublicInvoiceResponse{
		InvoiceNumber: inv.InvoiceNumber,
		MerchantName:  merchantName,
		CustomerName:  inv.CustomerName,
		Description:   inv.Description,
		Amount:        inv.Amount,
		Status:        string(inv.Status),
		DueDate:       inv.DueDate,
	}
}

func ToPaymentIntent(p *PaymentIntent) PaymentIntentResponse {
	return PaymentIntentResponse{
		ID:          p.ID.String(),
		InvoiceID:   p.InvoiceID.String(),
		Method:      string(p.Method),
		Status:      string(p.Status),
		Amount:      p.Amount,
		CreatedAt:   p.CreatedAt,
		ProcessedAt: p.ProcessedAt,
	}
}

func ToRefund(r *Refund) RefundResponse {
	return RefundResponse{
		ID:              r.ID.String(),
		InvoiceID:       r.InvoiceID.String(),
		PaymentIntentID: r.PaymentIntentID.String(),
		MerchantID:      r.MerchantID.String(),
		Amount:          r.Amount,
		Reason:          r.Reason,
		Status:          string(r.Status),
		CreatedAt:       r.CreatedAt,
		ProcessedAt:     r.ProcessedAt,
	}
}
