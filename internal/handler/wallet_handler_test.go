package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

func TestGetWallet_ScopedToCallingMerchant(t *testing.T) {
	e := newTestEnv()
	uid, bearer := e.tokenFor(t, constant.RoleMerchant)

	var served uuid.UUID
	e.wallet.balanceFn = func(_ context.Context, merchantID uuid.UUID) (*model.Wallet, error) {
		served = merchantID
		return &model.Wallet{MerchantID: merchantID, Balance: 7500}, nil
	}

	rec := e.do(t, http.MethodGet, "/api/v1/wallet", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	// The merchant id must come from the token, never from client input.
	if served != uid {
		t.Errorf("service queried merchant %s, want the token subject %s", served, uid)
	}

	var body model.WalletResponse
	decode(t, rec, &body)
	if body.Balance != 7500 {
		t.Errorf("balance = %d, want 7500", body.Balance)
	}
	if body.MerchantID != uid.String() {
		t.Errorf("merchant_id = %q, want %q", body.MerchantID, uid)
	}
}

func TestGetWallet_RequiresAuth(t *testing.T) {
	e := newTestEnv()
	rec := e.do(t, http.MethodGet, "/api/v1/wallet", "", nil)
	assertStatus(t, rec, http.StatusUnauthorized)
}

// SRS §2.2: top-up is a merchant capability; an admin token must be refused.
func TestGetWallet_AdminIsForbidden(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodGet, "/api/v1/wallet", bearer, nil)
	assertStatus(t, rec, http.StatusForbidden)
}

// SRS §2.2: a top-up starts life as PENDING and never credits immediately.
func TestRequestTopup_CreatedAsPending(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	rec := e.do(t, http.MethodPost, "/api/v1/wallet/topup", bearer, map[string]any{"amount": 25000})

	assertStatus(t, rec, http.StatusCreated)
	var body model.TopupResponse
	decode(t, rec, &body)
	if body.Status != string(constant.TopupPending) {
		t.Errorf("status = %q, want PENDING", body.Status)
	}
	if body.Amount != 25000 {
		t.Errorf("amount = %d, want 25000", body.Amount)
	}
	if body.ProcessedAt != nil {
		t.Error("processed_at must be absent on a pending top-up")
	}
}

// SRS §4.5: amount must be > 0.
func TestRequestTopup_RejectsNonPositiveAmount(t *testing.T) {
	cases := []struct {
		name string
		body any
	}{
		{"zero", map[string]any{"amount": 0}},
		{"negative", map[string]any{"amount": -100}},
		{"missing", map[string]any{}},
		{"non-numeric", map[string]any{"amount": "banyak"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleMerchant)

			rec := e.do(t, http.MethodPost, "/api/v1/wallet/topup", bearer, tc.body)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestListMyTopups_PaginationEnvelope(t *testing.T) {
	e := newTestEnv()
	uid, bearer := e.tokenFor(t, constant.RoleMerchant)

	e.wallet.listFn = func(_ context.Context, _ *uuid.UUID, _, _ int) ([]model.Topup, int64, error) {
		return []model.Topup{
			{ID: uuid.New(), MerchantID: uid, Amount: 1000, Status: constant.TopupSuccess},
			{ID: uuid.New(), MerchantID: uid, Amount: 2000, Status: constant.TopupPending},
		}, 7, nil
	}

	rec := e.do(t, http.MethodGet, "/api/v1/wallet/topups?page=2&page_size=2", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Data       []model.TopupResponse `json:"data"`
		Pagination struct {
			Page     int   `json:"page"`
			PageSize int   `json:"page_size"`
			Total    int64 `json:"total"`
		} `json:"pagination"`
	}
	decode(t, rec, &body)

	if len(body.Data) != 2 {
		t.Fatalf("data length = %d, want 2", len(body.Data))
	}
	if body.Pagination.Page != 2 || body.Pagination.PageSize != 2 || body.Pagination.Total != 7 {
		t.Errorf("unexpected pagination meta: %+v", body.Pagination)
	}
	// page=2, page_size=2 → offset 2, limit 2.
	if e.wallet.lastOffset != 2 || e.wallet.lastLimit != 2 {
		t.Errorf("offset/limit = %d/%d, want 2/2", e.wallet.lastOffset, e.wallet.lastLimit)
	}
	// A merchant may only ever see their own top-ups.
	if e.wallet.lastFilter == nil || *e.wallet.lastFilter != uid {
		t.Errorf("list was not scoped to the calling merchant: %v", e.wallet.lastFilter)
	}
}

// SRS §4.1: an empty list is a valid state and must serialize as [].
func TestListMyTopups_EmptyStateIsArray(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	rec := e.do(t, http.MethodGet, "/api/v1/wallet/topups", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	var raw map[string]any
	decode(t, rec, &raw)
	if data, ok := raw["data"].([]any); !ok || len(data) != 0 {
		t.Errorf(`data = %v, want an empty array`, raw["data"])
	}
}

// ── admin top-up processing ───────────────────────────────────────────────────

func TestAdminProcessTopup_SuccessCreditsAndReturnsSuccess(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	topupID := uuid.New()

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/topups/"+topupID.String(), bearer,
		map[string]any{"action": "SUCCESS"})

	assertStatus(t, rec, http.StatusOK)
	if e.wallet.lastSuccess == nil || !*e.wallet.lastSuccess {
		t.Fatalf("action SUCCESS must reach the service as success=true, got %v", e.wallet.lastSuccess)
	}
	var body model.TopupResponse
	decode(t, rec, &body)
	if body.Status != string(constant.TopupSuccess) {
		t.Errorf("status = %q, want SUCCESS", body.Status)
	}
}

func TestAdminProcessTopup_FailedMapsToFalse(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/topups/"+uuid.New().String(), bearer,
		map[string]any{"action": "FAILED"})

	assertStatus(t, rec, http.StatusOK)
	if e.wallet.lastSuccess == nil || *e.wallet.lastSuccess {
		t.Fatalf("action FAILED must reach the service as success=false, got %v", e.wallet.lastSuccess)
	}
}

// Only SUCCESS and FAILED exist in the top-up state machine.
func TestAdminProcessTopup_RejectsUnknownAction(t *testing.T) {
	for _, action := range []string{"success", "APPROVE", "PENDING", ""} {
		t.Run("action="+action, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleAdmin)

			rec := e.do(t, http.MethodPatch, "/api/v1/admin/topups/"+uuid.New().String(), bearer,
				map[string]any{"action": action})
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestAdminProcessTopup_RejectsMalformedID(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/topups/not-a-uuid", bearer,
		map[string]any{"action": "SUCCESS"})

	assertStatus(t, rec, http.StatusBadRequest)
	if code := errCode(t, rec); code != "INVALID_ID" {
		t.Errorf("error code = %q, want INVALID_ID", code)
	}
}

// Re-processing a settled top-up is an invalid transition → 422.
func TestAdminProcessTopup_AlreadySettledIsUnprocessable(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	e.wallet.processFn = func(context.Context, uuid.UUID, bool) (*model.Topup, error) {
		return nil, apperror.ErrInvalidState
	}

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/topups/"+uuid.New().String(), bearer,
		map[string]any{"action": "SUCCESS"})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	if code := errCode(t, rec); code != "INVALID_STATE" {
		t.Errorf("error code = %q, want INVALID_STATE", code)
	}
}

func TestAdminProcessTopup_MerchantIsForbidden(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/topups/"+uuid.New().String(), bearer,
		map[string]any{"action": "SUCCESS"})

	assertStatus(t, rec, http.StatusForbidden)
	if e.wallet.lastSuccess != nil {
		t.Fatal("a merchant must not be able to settle their own top-up")
	}
}

func TestAdminListTopups_FiltersByMerchant(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	merchantID := uuid.New()

	rec := e.do(t, http.MethodGet, "/api/v1/admin/topups?merchant_id="+merchantID.String(), bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	if e.wallet.lastFilter == nil || *e.wallet.lastFilter != merchantID {
		t.Errorf("merchant_id filter not forwarded: %v", e.wallet.lastFilter)
	}
}

func TestAdminListTopups_NoFilterMeansAllMerchants(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodGet, "/api/v1/admin/topups", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	if e.wallet.lastFilter != nil {
		t.Errorf("expected a nil merchant filter, got %v", e.wallet.lastFilter)
	}
}

func TestAdminListTopups_RejectsMalformedMerchantID(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodGet, "/api/v1/admin/topups?merchant_id=nope", bearer, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}
