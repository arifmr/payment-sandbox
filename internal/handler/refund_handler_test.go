package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/service"
)

// SRS §2.5: a merchant requests a refund; it starts in REQUESTED.
func TestRequestRefund_CreatedAsRequested(t *testing.T) {
	e := newTestEnv()
	uid, bearer := e.tokenFor(t, constant.RoleMerchant)
	invoiceID := uuid.New()

	rec := e.do(t, http.MethodPost, "/api/v1/refunds", bearer, map[string]any{
		"invoice_id": invoiceID.String(), "amount": 1500, "reason": "customer cancelled",
	})

	assertStatus(t, rec, http.StatusCreated)
	var body model.RefundResponse
	decode(t, rec, &body)
	if body.Status != string(constant.RefundRequested) {
		t.Errorf("status = %q, want REQUESTED", body.Status)
	}
	if body.Amount != 1500 {
		t.Errorf("amount = %d, want 1500", body.Amount)
	}
	// The requester is taken from the token, never from the payload.
	if e.refund.lastMerchantID != uid {
		t.Errorf("merchant id = %s, want the token subject %s", e.refund.lastMerchantID, uid)
	}
}

func TestRequestRefund_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing invoice id", map[string]any{"amount": 100}},
		{"invoice id not a uuid", map[string]any{"invoice_id": "abc", "amount": 100}},
		{"zero amount", map[string]any{"invoice_id": uuid.New().String(), "amount": 0}},
		{"negative amount", map[string]any{"invoice_id": uuid.New().String(), "amount": -50}},
		{"missing amount", map[string]any{"invoice_id": uuid.New().String()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleMerchant)

			rec := e.do(t, http.MethodPost, "/api/v1/refunds", bearer, tc.body)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

// SRS §2.5: only a PAID invoice can be refunded.
func TestRequestRefund_UnpaidInvoiceIsUnprocessable(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)
	e.refund.requestFn = func(context.Context, uuid.UUID, uuid.UUID, int64, string) (*model.Refund, error) {
		return nil, apperror.New(apperror.KindUnprocessable, "INVOICE_NOT_PAID", "only PAID invoices can be refunded")
	}

	rec := e.do(t, http.MethodPost, "/api/v1/refunds", bearer, map[string]any{
		"invoice_id": uuid.New().String(), "amount": 100,
	})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	if code := errCode(t, rec); code != "INVOICE_NOT_PAID" {
		t.Errorf("error code = %q, want INVOICE_NOT_PAID", code)
	}
}

// Refunding more than the invoice's remaining balance must be refused.
func TestRequestRefund_ExceedingRemainingBalanceIsRefused(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)
	e.refund.requestFn = func(context.Context, uuid.UUID, uuid.UUID, int64, string) (*model.Refund, error) {
		return nil, apperror.New(apperror.KindUnprocessable, "REFUND_EXCEEDS_INVOICE",
			"refund amount exceeds the invoice's remaining refundable balance")
	}

	rec := e.do(t, http.MethodPost, "/api/v1/refunds", bearer, map[string]any{
		"invoice_id": uuid.New().String(), "amount": 999999,
	})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	if code := errCode(t, rec); code != "REFUND_EXCEEDS_INVOICE" {
		t.Errorf("error code = %q, want REFUND_EXCEEDS_INVOICE", code)
	}
}

// A merchant must not be able to refund somebody else's invoice.
func TestRequestRefund_ForeignInvoiceIsForbidden(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)
	e.refund.requestFn = func(context.Context, uuid.UUID, uuid.UUID, int64, string) (*model.Refund, error) {
		return nil, apperror.ErrForbidden
	}

	rec := e.do(t, http.MethodPost, "/api/v1/refunds", bearer, map[string]any{
		"invoice_id": uuid.New().String(), "amount": 100,
	})

	assertStatus(t, rec, http.StatusForbidden)
}

func TestRequestRefund_RequiresMerchantRole(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodPost, "/api/v1/refunds", bearer, map[string]any{
		"invoice_id": uuid.New().String(), "amount": 100,
	})
	assertStatus(t, rec, http.StatusForbidden)
}

func TestListMyRefunds_ScopedToCaller(t *testing.T) {
	e := newTestEnv()
	uid, bearer := e.tokenFor(t, constant.RoleMerchant)

	e.refund.listMineFn = func(_ context.Context, merchantID uuid.UUID, _, _ int) ([]model.Refund, int64, error) {
		return []model.Refund{{ID: uuid.New(), MerchantID: merchantID, Amount: 500, Status: constant.RefundRequested}}, 1, nil
	}

	rec := e.do(t, http.MethodGet, "/api/v1/refunds", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	if e.refund.lastMerchantID != uid {
		t.Errorf("list scoped to %s, want the token subject %s", e.refund.lastMerchantID, uid)
	}
	var body struct {
		Data []model.RefundResponse `json:"data"`
	}
	decode(t, rec, &body)
	if len(body.Data) != 1 || body.Data[0].MerchantID != uid.String() {
		t.Errorf("unexpected data: %+v", body.Data)
	}
}

// ── admin refund state machine (SRS §2.5 / §4.4) ──────────────────────────────

func TestAdminRefundAction_MapsEachAction(t *testing.T) {
	cases := []struct {
		action     string
		wantAction service.RefundAction
		wantStatus constant.RefundStatus
	}{
		{"APPROVE", service.RefundActionApprove, constant.RefundApproved},
		{"REJECT", service.RefundActionReject, constant.RefundRejected},
		{"PROCESS", service.RefundActionProcess, constant.RefundSuccess},
		{"FAIL", service.RefundActionFail, constant.RefundFailed},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleAdmin)
			id := uuid.New()

			e.refund.actionFn = func(_ context.Context, gotID uuid.UUID, action service.RefundAction) (*model.Refund, error) {
				if gotID != id {
					t.Errorf("service received id %s, want %s", gotID, id)
				}
				return &model.Refund{ID: gotID, Amount: 500, Status: tc.wantStatus}, nil
			}

			rec := e.do(t, http.MethodPatch, "/api/v1/admin/refunds/"+id.String(), bearer,
				map[string]any{"action": tc.action})

			assertStatus(t, rec, http.StatusOK)
			if e.refund.lastAction != tc.wantAction {
				t.Errorf("service received action %q, want %q", e.refund.lastAction, tc.wantAction)
			}
			var body model.RefundResponse
			decode(t, rec, &body)
			if body.Status != string(tc.wantStatus) {
				t.Errorf("status = %q, want %s", body.Status, tc.wantStatus)
			}
		})
	}
}

func TestAdminRefundAction_RejectsUnknownAction(t *testing.T) {
	for _, action := range []string{"SUCCESS", "approve", "CANCEL", ""} {
		t.Run("action="+action, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleAdmin)

			rec := e.do(t, http.MethodPatch, "/api/v1/admin/refunds/"+uuid.New().String(), bearer,
				map[string]any{"action": action})
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

// SRS §3.4: PROCESS before APPROVE is not a legal transition.
func TestAdminRefundAction_IllegalTransitionIsUnprocessable(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	e.refund.actionFn = func(context.Context, uuid.UUID, service.RefundAction) (*model.Refund, error) {
		return nil, apperror.ErrInvalidState
	}

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/refunds/"+uuid.New().String(), bearer,
		map[string]any{"action": "PROCESS"})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	if code := errCode(t, rec); code != "INVALID_STATE" {
		t.Errorf("error code = %q, want INVALID_STATE", code)
	}
}

// Debiting a merchant below zero must fail rather than create a negative balance.
func TestAdminRefundAction_InsufficientFundsIsUnprocessable(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	e.refund.actionFn = func(context.Context, uuid.UUID, service.RefundAction) (*model.Refund, error) {
		return nil, apperror.ErrInsufficientFunds
	}

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/refunds/"+uuid.New().String(), bearer,
		map[string]any{"action": "PROCESS"})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	if code := errCode(t, rec); code != "INSUFFICIENT_FUNDS" {
		t.Errorf("error code = %q, want INSUFFICIENT_FUNDS", code)
	}
}

// SRS §2.5: approving and processing refunds is an admin-only capability.
func TestAdminRefundAction_MerchantIsForbidden(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/refunds/"+uuid.New().String(), bearer,
		map[string]any{"action": "APPROVE"})

	assertStatus(t, rec, http.StatusForbidden)
	if e.refund.lastAction != "" {
		t.Fatal("a merchant must not be able to approve their own refund")
	}
}

func TestAdminRefundAction_RejectsMalformedID(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/refunds/nope", bearer, map[string]any{"action": "APPROVE"})
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestAdminListRefunds_ReturnsAllMerchants(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	e.refund.listFn = func(context.Context, int, int) ([]model.Refund, int64, error) {
		return []model.Refund{
			{ID: uuid.New(), MerchantID: uuid.New(), Amount: 100, Status: constant.RefundRequested},
			{ID: uuid.New(), MerchantID: uuid.New(), Amount: 200, Status: constant.RefundApproved},
		}, 2, nil
	}

	rec := e.do(t, http.MethodGet, "/api/v1/admin/refunds", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Data []model.RefundResponse `json:"data"`
	}
	decode(t, rec, &body)
	if len(body.Data) != 2 {
		t.Fatalf("data length = %d, want 2", len(body.Data))
	}
}

func TestAdminListRefunds_MerchantIsForbidden(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	// /api/v1/refunds is the merchant's own list; /api/v1/admin/refunds is admin-only.
	rec := e.do(t, http.MethodGet, "/api/v1/admin/refunds", bearer, nil)
	assertStatus(t, rec, http.StatusForbidden)
}
