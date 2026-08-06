package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/pkg/jwt"
	"github.com/dboarif/payment-sandbox/internal/pkg/metrics"
	"github.com/dboarif/payment-sandbox/internal/repository"
	"github.com/dboarif/payment-sandbox/internal/service"
)

// These tests drive the real router — real middleware chain, real routes, real
// binding rules — with stubbed services. They cover the HTTP contract: paths,
// status codes, role gating, validation and response shape.

const handlerTestSecret = "handler-test-secret-at-least-32-chars"

func init() { gin.SetMode(gin.TestMode) }

// ── stub services ─────────────────────────────────────────────────────────────

type stubAuth struct {
	registerFn func(ctx context.Context, name, email, password string) (*model.User, error)
	loginFn    func(ctx context.Context, email, password string) (*service.TokenPair, error)
	refreshFn  func(ctx context.Context, token string) (*service.TokenPair, error)
	logoutFn   func(ctx context.Context, token string) error

	lastEmail    string
	lastPassword string
	logoutCalls  int
}

func (s *stubAuth) Register(ctx context.Context, name, email, password string) (*model.User, error) {
	s.lastEmail, s.lastPassword = email, password
	if s.registerFn != nil {
		return s.registerFn(ctx, name, email, password)
	}
	return &model.User{ID: uuid.New(), Name: name, Email: email, Role: constant.RoleMerchant}, nil
}

func (s *stubAuth) Login(ctx context.Context, email, password string) (*service.TokenPair, error) {
	s.lastEmail, s.lastPassword = email, password
	if s.loginFn != nil {
		return s.loginFn(ctx, email, password)
	}
	return newTokenPair(email), nil
}

func (s *stubAuth) Refresh(ctx context.Context, token string) (*service.TokenPair, error) {
	if s.refreshFn != nil {
		return s.refreshFn(ctx, token)
	}
	return newTokenPair("merchant@example.com"), nil
}

func (s *stubAuth) Logout(ctx context.Context, token string) error {
	s.logoutCalls++
	if s.logoutFn != nil {
		return s.logoutFn(ctx, token)
	}
	return nil
}

func newTokenPair(email string) *service.TokenPair {
	return &service.TokenPair{
		AccessToken:      "access-token",
		AccessExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshToken:     "refresh-token",
		RefreshExpiresAt: time.Now().Add(24 * time.Hour),
		User:             &model.User{ID: uuid.New(), Email: email, Name: "Toko A", Role: constant.RoleMerchant},
	}
}

type stubWallet struct {
	balanceFn   func(ctx context.Context, merchantID uuid.UUID) (*model.Wallet, error)
	topupFn     func(ctx context.Context, merchantID uuid.UUID, amount int64) (*model.Topup, error)
	processFn   func(ctx context.Context, topupID uuid.UUID, success bool) (*model.Topup, error)
	listFn      func(ctx context.Context, merchantID *uuid.UUID, offset, limit int) ([]model.Topup, int64, error)
	lastSuccess *bool
	lastOffset  int
	lastLimit   int
	lastFilter  *uuid.UUID
}

func (s *stubWallet) GetBalance(ctx context.Context, merchantID uuid.UUID) (*model.Wallet, error) {
	if s.balanceFn != nil {
		return s.balanceFn(ctx, merchantID)
	}
	return &model.Wallet{MerchantID: merchantID, Balance: 7500}, nil
}

func (s *stubWallet) RequestTopup(ctx context.Context, merchantID uuid.UUID, amount int64) (*model.Topup, error) {
	if s.topupFn != nil {
		return s.topupFn(ctx, merchantID, amount)
	}
	return &model.Topup{ID: uuid.New(), MerchantID: merchantID, Amount: amount, Status: constant.TopupPending}, nil
}

func (s *stubWallet) ProcessTopup(ctx context.Context, topupID uuid.UUID, success bool) (*model.Topup, error) {
	s.lastSuccess = &success
	if s.processFn != nil {
		return s.processFn(ctx, topupID, success)
	}
	status := constant.TopupFailed
	if success {
		status = constant.TopupSuccess
	}
	return &model.Topup{ID: topupID, Amount: 1000, Status: status}, nil
}

func (s *stubWallet) ListTopups(ctx context.Context, merchantID *uuid.UUID, offset, limit int) ([]model.Topup, int64, error) {
	s.lastFilter, s.lastOffset, s.lastLimit = merchantID, offset, limit
	if s.listFn != nil {
		return s.listFn(ctx, merchantID, offset, limit)
	}
	return []model.Topup{}, 0, nil
}

type stubInvoice struct {
	createFn func(ctx context.Context, in service.CreateInvoiceInput) (*model.Invoice, error)
	getFn    func(ctx context.Context, id, merchantID uuid.UUID) (*model.Invoice, error)
	tokenFn  func(ctx context.Context, token string) (*model.Invoice, error)
	listFn   func(ctx context.Context, f repository.InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error)

	lastInput  service.CreateInvoiceInput
	lastFilter repository.InvoiceFilter
	lastOffset int
	lastLimit  int
}

func (s *stubInvoice) Create(ctx context.Context, in service.CreateInvoiceInput) (*model.Invoice, error) {
	s.lastInput = in
	if s.createFn != nil {
		return s.createFn(ctx, in)
	}
	return &model.Invoice{
		ID: uuid.New(), InvoiceNumber: "INV-20260726-ABCDEF0123", MerchantID: in.MerchantID,
		CustomerName: in.CustomerName, Amount: in.Amount, Status: constant.InvoicePending,
		DueDate: in.DueDate, PaymentToken: "tok-abc",
	}, nil
}

func (s *stubInvoice) GetByID(ctx context.Context, id, merchantID uuid.UUID) (*model.Invoice, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id, merchantID)
	}
	return &model.Invoice{ID: id, MerchantID: merchantID, Amount: 5000, Status: constant.InvoicePending, PaymentToken: "tok-abc"}, nil
}

func (s *stubInvoice) GetByPaymentToken(ctx context.Context, token string) (*model.Invoice, error) {
	if s.tokenFn != nil {
		return s.tokenFn(ctx, token)
	}
	return &model.Invoice{
		ID: uuid.New(), InvoiceNumber: "INV-20260726-ABCDEF0123", MerchantID: uuid.New(),
		CustomerName: "Budi", CustomerEmail: "budi@example.com", Amount: 5000,
		Status: constant.InvoicePending, DueDate: time.Now().Add(time.Hour), PaymentToken: token,
	}, nil
}

func (s *stubInvoice) List(ctx context.Context, f repository.InvoiceFilter, offset, limit int) ([]model.Invoice, int64, error) {
	s.lastFilter, s.lastOffset, s.lastLimit = f, offset, limit
	if s.listFn != nil {
		return s.listFn(ctx, f, offset, limit)
	}
	return []model.Invoice{}, 0, nil
}

func (s *stubInvoice) ExpireDue(ctx context.Context) (service.ExpiryResult, error) {
	return service.ExpiryResult{}, nil
}

type stubPayment struct {
	createFn  func(ctx context.Context, token string, method constant.PaymentMethod, payer *uuid.UUID) (*model.PaymentIntent, error)
	processFn func(ctx context.Context, id uuid.UUID, success bool) (*model.PaymentIntent, error)
	getFn     func(ctx context.Context, id uuid.UUID) (*model.PaymentIntent, error)
	byTokenFn func(ctx context.Context, token string, id uuid.UUID) (*model.PaymentIntent, error)
	listFn    func(ctx context.Context, f repository.PaymentIntentFilter, offset, limit int) ([]model.PaymentIntent, int64, error)

	lastPayer   *uuid.UUID
	lastMethod  constant.PaymentMethod
	lastSuccess *bool
	lastFilter  repository.PaymentIntentFilter
}

func (s *stubPayment) CreateIntent(ctx context.Context, token string, method constant.PaymentMethod, payer *uuid.UUID) (*model.PaymentIntent, error) {
	s.lastPayer, s.lastMethod = payer, method
	if s.createFn != nil {
		return s.createFn(ctx, token, method, payer)
	}
	return &model.PaymentIntent{
		ID: uuid.New(), InvoiceID: uuid.New(), Method: method,
		Status: constant.PaymentPending, Amount: 5000, PayerUserID: payer,
	}, nil
}

func (s *stubPayment) Process(ctx context.Context, id uuid.UUID, success bool) (*model.PaymentIntent, error) {
	s.lastSuccess = &success
	if s.processFn != nil {
		return s.processFn(ctx, id, success)
	}
	status := constant.PaymentFailed
	if success {
		status = constant.PaymentSuccess
	}
	return &model.PaymentIntent{ID: id, InvoiceID: uuid.New(), Method: constant.PaymentMethodVADummy, Status: status, Amount: 5000}, nil
}

func (s *stubPayment) GetIntent(ctx context.Context, id uuid.UUID) (*model.PaymentIntent, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return &model.PaymentIntent{ID: id, InvoiceID: uuid.New(), Method: constant.PaymentMethodWallet, Status: constant.PaymentPending, Amount: 5000}, nil
}

func (s *stubPayment) GetIntentByToken(ctx context.Context, token string, id uuid.UUID) (*model.PaymentIntent, error) {
	if s.byTokenFn != nil {
		return s.byTokenFn(ctx, token, id)
	}
	return &model.PaymentIntent{ID: id, InvoiceID: uuid.New(), Method: constant.PaymentMethodWallet, Status: constant.PaymentPending, Amount: 5000}, nil
}

func (s *stubPayment) ListIntents(ctx context.Context, f repository.PaymentIntentFilter, offset, limit int) ([]model.PaymentIntent, int64, error) {
	s.lastFilter = f
	if s.listFn != nil {
		return s.listFn(ctx, f, offset, limit)
	}
	return []model.PaymentIntent{}, 0, nil
}

type stubRefund struct {
	requestFn  func(ctx context.Context, merchantID, invoiceID uuid.UUID, amount int64, reason string) (*model.Refund, error)
	actionFn   func(ctx context.Context, id uuid.UUID, action service.RefundAction) (*model.Refund, error)
	listMineFn func(ctx context.Context, merchantID uuid.UUID, offset, limit int) ([]model.Refund, int64, error)
	listFn     func(ctx context.Context, offset, limit int) ([]model.Refund, int64, error)

	lastAction     service.RefundAction
	lastMerchantID uuid.UUID
	lastAmount     int64
}

func (s *stubRefund) Request(ctx context.Context, merchantID, invoiceID uuid.UUID, amount int64, reason string) (*model.Refund, error) {
	s.lastMerchantID, s.lastAmount = merchantID, amount
	if s.requestFn != nil {
		return s.requestFn(ctx, merchantID, invoiceID, amount, reason)
	}
	return &model.Refund{
		ID: uuid.New(), InvoiceID: invoiceID, PaymentIntentID: uuid.New(), MerchantID: merchantID,
		Amount: amount, Reason: reason, Status: constant.RefundRequested,
	}, nil
}

func (s *stubRefund) AdminAction(ctx context.Context, id uuid.UUID, action service.RefundAction) (*model.Refund, error) {
	s.lastAction = action
	if s.actionFn != nil {
		return s.actionFn(ctx, id, action)
	}
	return &model.Refund{ID: id, InvoiceID: uuid.New(), PaymentIntentID: uuid.New(), MerchantID: uuid.New(), Amount: 500, Status: constant.RefundApproved}, nil
}

func (s *stubRefund) ListByMerchant(ctx context.Context, merchantID uuid.UUID, offset, limit int) ([]model.Refund, int64, error) {
	s.lastMerchantID = merchantID
	if s.listMineFn != nil {
		return s.listMineFn(ctx, merchantID, offset, limit)
	}
	return []model.Refund{}, 0, nil
}

func (s *stubRefund) List(ctx context.Context, offset, limit int) ([]model.Refund, int64, error) {
	if s.listFn != nil {
		return s.listFn(ctx, offset, limit)
	}
	return []model.Refund{}, 0, nil
}

func (s *stubRefund) GetByID(ctx context.Context, id uuid.UUID) (*model.Refund, error) {
	return &model.Refund{ID: id, Status: constant.RefundRequested}, nil
}

type stubAdmin struct {
	statsFn    func(ctx context.Context, f repository.DashboardFilter) (*repository.DashboardStats, error)
	lastFilter repository.DashboardFilter
}

func (s *stubAdmin) Dashboard(ctx context.Context, f repository.DashboardFilter) (*repository.DashboardStats, error) {
	s.lastFilter = f
	if s.statsFn != nil {
		return s.statsFn(ctx, f)
	}
	return &repository.DashboardStats{
		TotalInvoices: 10, TotalPaid: 6, TotalFailed: 2, TotalExpired: 1,
		TotalAmountPaid: 60000, TotalAmountRefund: 5000,
	}, nil
}

type stubUserRepo struct {
	findByIDFn func(ctx context.Context, id uuid.UUID) (*model.User, error)
}

func (s *stubUserRepo) Create(context.Context, *model.User) error { return nil }
func (s *stubUserRepo) FindByEmail(context.Context, string) (*model.User, error) {
	return nil, apperror.ErrNotFound
}
func (s *stubUserRepo) FindByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	if s.findByIDFn != nil {
		return s.findByIDFn(ctx, id)
	}
	return &model.User{ID: id, Name: "Toko A", Email: "toko@example.com", Role: constant.RoleMerchant}, nil
}

// Compile-time proof the stubs still satisfy the interfaces they stand in for.
var (
	_ service.AuthService       = (*stubAuth)(nil)
	_ service.WalletService     = (*stubWallet)(nil)
	_ service.InvoiceService    = (*stubInvoice)(nil)
	_ service.PaymentService    = (*stubPayment)(nil)
	_ service.RefundService     = (*stubRefund)(nil)
	_ service.AdminService      = (*stubAdmin)(nil)
	_ repository.UserRepository = (*stubUserRepo)(nil)
)

// ── test harness ──────────────────────────────────────────────────────────────

type testEnv struct {
	router  *gin.Engine
	auth    *stubAuth
	wallet  *stubWallet
	invoice *stubInvoice
	payment *stubPayment
	refund  *stubRefund
	admin   *stubAdmin
	users   *stubUserRepo
	jwt     *jwt.Manager
	pinger  *stubPinger
	metrics *metrics.Registry
}

// stubPinger stands in for *sql.DB in the readiness probe.
type stubPinger struct{ err error }

func (s *stubPinger) PingContext(context.Context) error { return s.err }

var _ Pinger = (*stubPinger)(nil)

func newTestEnv() *testEnv {
	e := &testEnv{
		auth:    &stubAuth{},
		wallet:  &stubWallet{},
		invoice: &stubInvoice{},
		payment: &stubPayment{},
		refund:  &stubRefund{},
		admin:   &stubAdmin{},
		users:   &stubUserRepo{},
		jwt:     jwt.New(handlerTestSecret, time.Hour),
		pinger:  &stubPinger{},
		metrics: metrics.NewRegistry(),
	}
	hs := &Handlers{
		Auth:    NewAuthHandler(e.auth, nil),
		Wallet:  NewWalletHandler(e.wallet),
		Invoice: NewInvoiceHandler(e.invoice, "/api/v1/pay"),
		Payment: NewPaymentHandler(e.payment, e.invoice, e.users),
		Refund:  NewRefundHandler(e.refund),
		Admin:   NewAdminHandler(e.admin),
		Health:  NewHealthHandler(e.pinger, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	e.router = NewRouter(hs, e.jwt, slog.New(slog.NewTextHandler(io.Discard, nil)), RouterDeps{
		Metrics:       e.metrics,
		ExposeMetrics: true,
	})
	return e
}

// tokenFor mints a bearer header for the given role.
func (e *testEnv) tokenFor(t *testing.T, role constant.Role) (uuid.UUID, string) {
	t.Helper()
	uid := uuid.New()
	tok, _, err := e.jwt.Issue(uid.String(), string(role))
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}
	return uid, "Bearer " + tok
}

// do performs a request. body may be nil, a string (sent verbatim) or any value
// to be JSON-encoded.
func (e *testEnv) do(t *testing.T, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = bytes.NewBufferString(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("encoding body: %v", err)
		}
		reader = bytes.NewBuffer(raw)
	}

	req := httptest.NewRequest(method, path, reader)
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// decode unmarshals a successful JSON response into out.
func decode(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
}

// errCode extracts error.code from a failure response.
func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body model.ErrorResponse
	decode(t, rec, &body)
	return body.Error.Code
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, want, rec.Body.String())
	}
}
