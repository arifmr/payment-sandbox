package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/repository"
)

// ── public payment page (SRS §2.4 / §4.3) ─────────────────────────────────────

func TestGetPublicInvoice_NoAuthNeeded(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodGet, "/api/v1/pay/tok-abc", "", nil)

	assertStatus(t, rec, http.StatusOK)
	var body model.PublicInvoiceResponse
	decode(t, rec, &body)
	if body.InvoiceNumber == "" || body.Amount == 0 {
		t.Errorf("payment page needs the invoice details: %+v", body)
	}
	if body.MerchantName == "" {
		t.Error("merchant_name must be resolved for the payment page")
	}
	if body.Status != string(constant.InvoicePending) {
		t.Errorf("status = %q, want PENDING", body.Status)
	}
}

// SRS §5.2: the public page must not expose data the payer has no business seeing.
func TestGetPublicInvoice_DoesNotLeakInternalFields(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodGet, "/api/v1/pay/tok-abc", "", nil)
	assertStatus(t, rec, http.StatusOK)

	body := rec.Body.String()
	for _, leaked := range []string{"merchant_id", "payment_token", "customer_email", "paid_at"} {
		if strings.Contains(body, leaked) {
			t.Errorf("public payment response leaks %q: %s", leaked, body)
		}
	}
}

func TestGetPublicInvoice_UnknownTokenIsNotFound(t *testing.T) {
	e := newTestEnv()
	e.invoice.tokenFn = func(context.Context, string) (*model.Invoice, error) {
		return nil, apperror.ErrNotFound
	}

	rec := e.do(t, http.MethodGet, "/api/v1/pay/does-not-exist", "", nil)
	assertStatus(t, rec, http.StatusNotFound)
}

// ── create intent ─────────────────────────────────────────────────────────────

// SRS §2.4: a payment intent starts as PENDING for every supported method.
func TestCreateIntent_PendingForEachMethod(t *testing.T) {
	for _, method := range []string{"WALLET", "VA_DUMMY", "EWALLET_DUMMY"} {
		t.Run(method, func(t *testing.T) {
			e := newTestEnv()

			rec := e.do(t, http.MethodPost, "/api/v1/pay/tok-abc", "", map[string]any{"method": method})

			assertStatus(t, rec, http.StatusCreated)
			var body model.PaymentIntentResponse
			decode(t, rec, &body)
			if body.Status != string(constant.PaymentPending) {
				t.Errorf("status = %q, want PENDING", body.Status)
			}
			if body.Method != method {
				t.Errorf("method = %q, want %q", body.Method, method)
			}
			if e.payment.lastMethod != constant.PaymentMethod(method) {
				t.Errorf("service received method %q", e.payment.lastMethod)
			}
		})
	}
}

// SRS §2.4 fixes the method list; anything else is a 400.
func TestCreateIntent_RejectsUnsupportedMethod(t *testing.T) {
	for _, method := range []string{"CREDIT_CARD", "wallet", "", "VA"} {
		t.Run("method="+method, func(t *testing.T) {
			e := newTestEnv()

			rec := e.do(t, http.MethodPost, "/api/v1/pay/tok-abc", "", map[string]any{"method": method})
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestCreateIntent_RejectsMissingBody(t *testing.T) {
	e := newTestEnv()
	rec := e.do(t, http.MethodPost, "/api/v1/pay/tok-abc", "", map[string]any{})
	assertStatus(t, rec, http.StatusBadRequest)
}

// Auth is optional here: an anonymous payer gets no payer id attached.
func TestCreateIntent_AnonymousPayerHasNoPayerID(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodPost, "/api/v1/pay/tok-abc", "", map[string]any{"method": "VA_DUMMY"})

	assertStatus(t, rec, http.StatusCreated)
	if e.payment.lastPayer != nil {
		t.Errorf("payer id = %v, want nil for an anonymous payer", e.payment.lastPayer)
	}
}

// A logged-in payer must be identified so a WALLET payment can debit them.
func TestCreateIntent_LoggedInPayerIsAttached(t *testing.T) {
	e := newTestEnv()
	uid, bearer := e.tokenFor(t, constant.RoleMerchant)

	rec := e.do(t, http.MethodPost, "/api/v1/pay/tok-abc", bearer, map[string]any{"method": "WALLET"})

	assertStatus(t, rec, http.StatusCreated)
	if e.payment.lastPayer == nil || *e.payment.lastPayer != uid {
		t.Errorf("payer id = %v, want %s", e.payment.lastPayer, uid)
	}
}

// An invalid token on an optional-auth route is still an error, not "anonymous".
func TestCreateIntent_InvalidTokenIsUnauthorized(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodPost, "/api/v1/pay/tok-abc", "Bearer tampered.jwt.value",
		map[string]any{"method": "WALLET"})

	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestCreateIntent_PaidInvoiceIsUnprocessable(t *testing.T) {
	e := newTestEnv()
	e.payment.createFn = func(context.Context, string, constant.PaymentMethod, *uuid.UUID) (*model.PaymentIntent, error) {
		return nil, apperror.ErrInvoiceNotPayable
	}

	rec := e.do(t, http.MethodPost, "/api/v1/pay/tok-abc", "", map[string]any{"method": "VA_DUMMY"})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	if code := errCode(t, rec); code != "INVOICE_NOT_PAYABLE" {
		t.Errorf("error code = %q, want INVOICE_NOT_PAYABLE", code)
	}
}

// SRS §2.4: paying an overdue invoice must be refused.
func TestCreateIntent_ExpiredInvoiceIsUnprocessable(t *testing.T) {
	e := newTestEnv()
	e.payment.createFn = func(context.Context, string, constant.PaymentMethod, *uuid.UUID) (*model.PaymentIntent, error) {
		return nil, apperror.ErrInvoiceExpired
	}

	rec := e.do(t, http.MethodPost, "/api/v1/pay/tok-abc", "", map[string]any{"method": "VA_DUMMY"})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
	if code := errCode(t, rec); code != "INVOICE_EXPIRED" {
		t.Errorf("error code = %q, want INVOICE_EXPIRED", code)
	}
}

// ── payer polls their status (SRS §4.3) ───────────────────────────────────────

func TestGetIntentByToken_ReturnsStatus(t *testing.T) {
	e := newTestEnv()
	id := uuid.New()

	rec := e.do(t, http.MethodGet, "/api/v1/pay/tok-abc/intents/"+id.String(), "", nil)

	assertStatus(t, rec, http.StatusOK)
	var body model.PaymentIntentResponse
	decode(t, rec, &body)
	if body.ID != id.String() {
		t.Errorf("id = %q, want %q", body.ID, id)
	}
	if body.Status == "" {
		t.Error("status must be present so the payer can poll it")
	}
}

// An intent belonging to a different invoice must look like it does not exist.
func TestGetIntentByToken_ForeignIntentIsNotFound(t *testing.T) {
	e := newTestEnv()
	e.payment.byTokenFn = func(context.Context, string, uuid.UUID) (*model.PaymentIntent, error) {
		return nil, apperror.ErrNotFound
	}

	rec := e.do(t, http.MethodGet, "/api/v1/pay/tok-abc/intents/"+uuid.New().String(), "", nil)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestGetIntentByToken_RejectsMalformedID(t *testing.T) {
	e := newTestEnv()
	rec := e.do(t, http.MethodGet, "/api/v1/pay/tok-abc/intents/nope", "", nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

// ── admin simulation panel (SRS §2.4 / §4.4) ──────────────────────────────────

func TestAdminProcessPayment_SuccessAndFailedMapping(t *testing.T) {
	cases := []struct {
		action      string
		wantSuccess bool
		wantStatus  constant.PaymentIntentStatus
	}{
		{"SUCCESS", true, constant.PaymentSuccess},
		{"FAILED", false, constant.PaymentFailed},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleAdmin)

			rec := e.do(t, http.MethodPatch, "/api/v1/admin/payments/"+uuid.New().String(), bearer,
				map[string]any{"action": tc.action})

			assertStatus(t, rec, http.StatusOK)
			if e.payment.lastSuccess == nil || *e.payment.lastSuccess != tc.wantSuccess {
				t.Fatalf("service received success=%v, want %v", e.payment.lastSuccess, tc.wantSuccess)
			}
			var body model.PaymentIntentResponse
			decode(t, rec, &body)
			if body.Status != string(tc.wantStatus) {
				t.Errorf("status = %q, want %s", body.Status, tc.wantStatus)
			}
		})
	}
}

func TestAdminProcessPayment_RejectsUnknownAction(t *testing.T) {
	for _, action := range []string{"PENDING", "APPROVE", "success", ""} {
		t.Run("action="+action, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleAdmin)

			rec := e.do(t, http.MethodPatch, "/api/v1/admin/payments/"+uuid.New().String(), bearer,
				map[string]any{"action": action})
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

// SRS §2.4: only an admin may finalize a payment.
func TestAdminProcessPayment_MerchantIsForbidden(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/payments/"+uuid.New().String(), bearer,
		map[string]any{"action": "SUCCESS"})

	assertStatus(t, rec, http.StatusForbidden)
	if e.payment.lastSuccess != nil {
		t.Fatal("a merchant must not be able to mark their own payment SUCCESS")
	}
}

func TestAdminProcessPayment_AnonymousIsUnauthorized(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/payments/"+uuid.New().String(), "",
		map[string]any{"action": "SUCCESS"})

	assertStatus(t, rec, http.StatusUnauthorized)
	if e.payment.lastSuccess != nil {
		t.Fatal("an anonymous caller must not reach the payment simulation")
	}
}

// Re-finalizing a settled intent is an invalid transition.
func TestAdminProcessPayment_AlreadySettledIsUnprocessable(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	e.payment.processFn = func(context.Context, uuid.UUID, bool) (*model.PaymentIntent, error) {
		return nil, apperror.ErrInvalidState
	}

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/payments/"+uuid.New().String(), bearer,
		map[string]any{"action": "SUCCESS"})

	assertStatus(t, rec, http.StatusUnprocessableEntity)
}

func TestAdminProcessPayment_RejectsMalformedID(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodPatch, "/api/v1/admin/payments/xyz", bearer, map[string]any{"action": "SUCCESS"})
	assertStatus(t, rec, http.StatusBadRequest)
}

// SRS §4.4: the admin panel needs to search intents before acting on them.
func TestAdminListIntents_FiltersByInvoiceAndStatus(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	invoiceID := uuid.New()

	rec := e.do(t, http.MethodGet,
		"/api/v1/admin/payments?invoice_id="+invoiceID.String()+"&status=PENDING", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	f := e.payment.lastFilter
	if f.InvoiceID == nil || *f.InvoiceID != invoiceID {
		t.Errorf("invoice_id filter = %v, want %s", f.InvoiceID, invoiceID)
	}
	if f.Status == nil || *f.Status != constant.PaymentPending {
		t.Errorf("status filter = %v, want PENDING", f.Status)
	}
}

func TestAdminListIntents_RejectsInvalidFilters(t *testing.T) {
	cases := []struct{ name, query string }{
		{"bad invoice id", "invoice_id=not-a-uuid"},
		{"unknown status", "status=CANCELLED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleAdmin)

			rec := e.do(t, http.MethodGet, "/api/v1/admin/payments?"+tc.query, bearer, nil)
			assertStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestAdminListIntents_ReturnsPaginatedItems(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	now := time.Now()

	e.payment.listFn = func(context.Context, repository.PaymentIntentFilter, int, int) ([]model.PaymentIntent, int64, error) {
		return []model.PaymentIntent{
			{ID: uuid.New(), InvoiceID: uuid.New(), Method: constant.PaymentMethodWallet, Status: constant.PaymentPending, Amount: 1000, CreatedAt: now},
		}, 1, nil
	}

	rec := e.do(t, http.MethodGet, "/api/v1/admin/payments", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Data       []model.PaymentIntentResponse `json:"data"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	decode(t, rec, &body)
	if len(body.Data) != 1 || body.Pagination.Total != 1 {
		t.Fatalf("unexpected payload: %+v", body)
	}
}

func TestAdminListIntents_MerchantIsForbidden(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	rec := e.do(t, http.MethodGet, "/api/v1/admin/payments", bearer, nil)
	assertStatus(t, rec, http.StatusForbidden)
}

func TestAdminGetIntent_OK(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	id := uuid.New()

	rec := e.do(t, http.MethodGet, "/api/v1/admin/payments/"+id.String(), bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	var body model.PaymentIntentResponse
	decode(t, rec, &body)
	if body.ID != id.String() {
		t.Errorf("id = %q, want %q", body.ID, id)
	}
}

func TestAdminGetIntent_NotFound(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	e.payment.getFn = func(context.Context, uuid.UUID) (*model.PaymentIntent, error) {
		return nil, apperror.ErrNotFound
	}

	rec := e.do(t, http.MethodGet, "/api/v1/admin/payments/"+uuid.New().String(), bearer, nil)
	assertStatus(t, rec, http.StatusNotFound)
}
