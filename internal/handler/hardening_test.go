package handler

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/pkg/jwt"
	"github.com/dboarif/payment-sandbox/internal/pkg/metrics"
	"github.com/dboarif/payment-sandbox/internal/pkg/ratelimit"
)

// newLimitedEnv builds a router with rate limiting switched on. ipRate/loginRate are
// per-minute allowances; a rate of 0 disables that dimension.
func newLimitedEnv(ipRate, ipBurst, loginRate, loginBurst int) *testEnv {
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
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	hs := &Handlers{
		Auth:    NewAuthHandler(e.auth, ratelimit.New(loginRate, time.Minute, loginBurst)),
		Wallet:  NewWalletHandler(e.wallet),
		Invoice: NewInvoiceHandler(e.invoice, "/api/v1/pay"),
		Payment: NewPaymentHandler(e.payment, e.invoice, e.users),
		Refund:  NewRefundHandler(e.refund),
		Admin:   NewAdminHandler(e.admin),
		Health:  NewHealthHandler(e.pinger, discard),
	}
	e.router = NewRouter(hs, e.jwt, discard, RouterDeps{
		AuthIPLimiter: ratelimit.New(ipRate, time.Minute, ipBurst),
		Metrics:       e.metrics,
		ExposeMetrics: true,
	})
	return e
}

func loginBody(email string) map[string]any {
	return map[string]any{"email": email, "password": "password123"}
}

// ── per-IP limiting ───────────────────────────────────────────────────────────

func TestAuthRateLimit_BlocksAfterBurst(t *testing.T) {
	// Burst of 3, and a slow refill so nothing comes back during the test.
	e := newLimitedEnv(3, 3, 0, 0)

	for i := 1; i <= 3; i++ {
		rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate limited but should be within the burst", i)
		}
	}

	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))
	assertStatus(t, rec, http.StatusTooManyRequests)
	if code := errCode(t, rec); code != "RATE_LIMITED" {
		t.Errorf("error code = %q, want RATE_LIMITED", code)
	}
}

// A 429 must tell the client when to come back, or a well-behaved client has no
// choice but to keep hammering.
func TestAuthRateLimit_SetsRetryAfter(t *testing.T) {
	e := newLimitedEnv(60, 1, 0, 0) // 1/sec, burst 1

	e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))

	assertStatus(t, rec, http.StatusTooManyRequests)
	retry := rec.Header().Get("Retry-After")
	if retry == "" {
		t.Fatal("Retry-After header is missing on a 429")
	}
	if n, err := strconv.Atoi(retry); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive integer number of seconds", retry)
	}
}

// The limit covers every auth endpoint, not just login: register and refresh are
// equally reachable without credentials.
func TestAuthRateLimit_AppliesToAllAuthEndpoints(t *testing.T) {
	for _, path := range []string{
		"/api/v1/auth/login",
		"/api/v1/auth/register",
		"/api/v1/auth/refresh",
		"/api/v1/auth/logout",
	} {
		t.Run(path, func(t *testing.T) {
			e := newLimitedEnv(1, 1, 0, 0)

			e.do(t, http.MethodPost, path, "", map[string]any{})
			rec := e.do(t, http.MethodPost, path, "", map[string]any{})
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("status = %d, want 429 on the second request", rec.Code)
			}
		})
	}
}

// Authenticated business endpoints are not IP-limited: they are already gated by a
// token, and throttling a legitimate merchant's dashboard would be a self-inflicted
// outage.
func TestAuthRateLimit_DoesNotApplyToBusinessEndpoints(t *testing.T) {
	e := newLimitedEnv(1, 1, 0, 0)
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	for i := 0; i < 5; i++ {
		rec := e.do(t, http.MethodGet, "/api/v1/invoices", bearer, nil)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d to /invoices was rate limited; only auth routes should be", i+1)
		}
	}
}

// Health probes must never be throttled — an orchestrator polls them constantly, and a
// 429 there would look like an outage.
func TestAuthRateLimit_DoesNotApplyToHealthProbes(t *testing.T) {
	e := newLimitedEnv(1, 1, 0, 0)

	for _, path := range []string{"/healthz", "/readyz"} {
		for i := 0; i < 5; i++ {
			rec := e.do(t, http.MethodGet, path, "", nil)
			if rec.Code == http.StatusTooManyRequests {
				t.Fatalf("%s was rate limited on poll %d", path, i+1)
			}
		}
	}
}

// With limiting disabled the router must behave exactly as before.
func TestAuthRateLimit_DisabledLetsEverythingThrough(t *testing.T) {
	e := newLimitedEnv(0, 0, 0, 0)

	for i := 0; i < 50; i++ {
		rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d limited although limiting is disabled", i+1)
		}
	}
}

// ── per-account limiting ──────────────────────────────────────────────────────
//
// Per-IP alone does not stop a botnet grinding one account from many addresses, and
// per-account alone does not stop one host trying many accounts. Both dimensions exist.

func TestLoginRateLimit_IsPerAccount(t *testing.T) {
	// IP limiting off so only the per-email budget can trigger.
	e := newLimitedEnv(0, 0, 2, 2)

	for i := 1; i <= 2; i++ {
		rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("victim@example.com"))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d on the victim account should be within budget", i)
		}
	}

	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("victim@example.com"))
	assertStatus(t, rec, http.StatusTooManyRequests)

	// A different account is unaffected — otherwise one attacked account would lock
	// out the whole platform.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("someone-else@example.com"))
	if rec.Code == http.StatusTooManyRequests {
		t.Error("a different account must have its own budget")
	}
}

// Case variation must not multiply an attacker's allowance: the limiter key is
// lower-cased, matching how the auth service normalises the email before lookup.
func TestLoginRateLimit_KeyIsCaseInsensitive(t *testing.T) {
	e := newLimitedEnv(0, 0, 2, 2)

	e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("victim@example.com"))
	e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("VICTIM@example.com"))

	// Same account either way, so the budget of 2 is already spent.
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("Victim@Example.com"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 — case variants must share one bucket", rec.Code)
	}
}

// Whitespace-padded addresses never reach the limiter: the `email` binding rule rejects
// them first. Pinned so it stays a deliberate 400 rather than silently becoming a
// separate rate-limit bucket if validation is ever loosened.
func TestLogin_PaddedEmailIsRejectedByValidation(t *testing.T) {
	e := newLimitedEnv(0, 0, 2, 2)

	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("  victim@example.com  "))
	assertStatus(t, rec, http.StatusBadRequest)
}

// The per-account check runs after binding, so a malformed body is still a 400 rather
// than consuming budget under an empty key.
func TestLoginRateLimit_MalformedBodyIsStillBadRequest(t *testing.T) {
	e := newLimitedEnv(0, 0, 1, 1)

	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", `{"email":`)
	assertStatus(t, rec, http.StatusBadRequest)

	// A valid attempt afterwards must still have its budget.
	rec = e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))
	if rec.Code == http.StatusTooManyRequests {
		t.Error("a malformed request consumed the account's budget")
	}
}

// A rate-limited login must not reach the service — otherwise bcrypt still runs and
// the limit protects nothing.
func TestLoginRateLimit_ShortCircuitsBeforeTheService(t *testing.T) {
	e := newLimitedEnv(0, 0, 1, 1)

	e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))
	e.auth.lastEmail = "" // reset the spy

	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))
	assertStatus(t, rec, http.StatusTooManyRequests)
	if e.auth.lastEmail != "" {
		t.Error("the auth service was called despite the request being rate limited")
	}
}

// ── metrics ───────────────────────────────────────────────────────────────────

func TestMetrics_RecordsRequests(t *testing.T) {
	e := newTestEnv()

	e.do(t, http.MethodGet, "/healthz", "", nil)
	e.do(t, http.MethodGet, "/api/v1/wallet", "", nil) // 401

	snap := e.metrics.Snapshot()
	if snap.Total < 2 {
		t.Errorf("observed %d requests, want at least 2", snap.Total)
	}
	// Latency on the failure path is exactly what matters during an incident, so the
	// 401 must be measured too.
	if out := e.metrics.Render(); !strings.Contains(out, `status="401"`) {
		t.Errorf("error responses should be measured:\n%s", out)
	}
}

// SRS §5.1 gives a 300 ms target; the /metrics output must make it readable.
func TestMetrics_ExposesSLORatio(t *testing.T) {
	e := newTestEnv()
	e.do(t, http.MethodGet, "/healthz", "", nil)

	rec := e.do(t, http.MethodGet, "/metrics", "", nil)
	assertStatus(t, rec, http.StatusOK)

	body := rec.Body.String()
	for _, want := range []string{
		"http_request_duration_seconds_bucket",
		"http_requests_within_slo_ratio",
		`le="0.3"`, // the §5.1 boundary
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q", want)
		}
	}
}

// Route labels must use the matched pattern, never the concrete path: one series per
// invoice id would be unbounded cardinality.
func TestMetrics_LabelsByRoutePatternNotConcretePath(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	first := "11111111-1111-1111-1111-111111111111"
	second := "22222222-2222-2222-2222-222222222222"
	e.do(t, http.MethodGet, "/api/v1/invoices/"+first, bearer, nil)
	e.do(t, http.MethodGet, "/api/v1/invoices/"+second, bearer, nil)

	out := e.metrics.Render()
	if !strings.Contains(out, `route="/api/v1/invoices/:id"`) {
		t.Errorf("expected the route pattern as the label:\n%s", out)
	}
	for _, id := range []string{first, second} {
		if strings.Contains(out, id) {
			t.Errorf("concrete id %s leaked into a metric label — unbounded cardinality", id)
		}
	}
	if !strings.Contains(out, `http_request_duration_seconds_count{method="GET",route="/api/v1/invoices/:id",status="200"} 2`) {
		t.Errorf("both requests should share one series:\n%s", out)
	}
}

// Unmatched paths collapse into a single series, so a 404 scan cannot inflate
// cardinality either.
func TestMetrics_UnmatchedPathsShareOneSeries(t *testing.T) {
	e := newTestEnv()

	e.do(t, http.MethodGet, "/api/v1/nope-one", "", nil)
	e.do(t, http.MethodGet, "/api/v1/nope-two", "", nil)

	out := e.metrics.Render()
	if !strings.Contains(out, `route="unmatched"`) {
		t.Errorf("expected an \"unmatched\" series:\n%s", out)
	}
	if strings.Contains(out, "nope-one") || strings.Contains(out, "nope-two") {
		t.Error("unmatched concrete paths must not become labels")
	}
}

// /metrics reveals route inventory and traffic shape, so it is opt-in.
func TestMetrics_EndpointNotServedWhenDisabled(t *testing.T) {
	e := &testEnv{
		auth: &stubAuth{}, wallet: &stubWallet{}, invoice: &stubInvoice{},
		payment: &stubPayment{}, refund: &stubRefund{}, admin: &stubAdmin{},
		users: &stubUserRepo{}, jwt: jwt.New(handlerTestSecret, time.Hour),
		pinger: &stubPinger{}, metrics: metrics.NewRegistry(),
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	hs := &Handlers{
		Auth: NewAuthHandler(e.auth, nil), Wallet: NewWalletHandler(e.wallet),
		Invoice: NewInvoiceHandler(e.invoice, "/api/v1/pay"),
		Payment: NewPaymentHandler(e.payment, e.invoice, e.users),
		Refund:  NewRefundHandler(e.refund), Admin: NewAdminHandler(e.admin),
		Health: NewHealthHandler(e.pinger, discard),
	}
	e.router = NewRouter(hs, e.jwt, discard, RouterDeps{
		Metrics:       e.metrics,
		ExposeMetrics: false,
	})

	rec := e.do(t, http.MethodGet, "/metrics", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when ExposeMetrics is off", rec.Code)
	}

	// Collection still happens; only publication is off.
	e.do(t, http.MethodGet, "/healthz", "", nil)
	if e.metrics.Snapshot().Total == 0 {
		t.Error("metrics should still be collected even when the endpoint is not exposed")
	}
}

// Every optional dependency may be nil, so the router stays usable in a minimal wiring.
func TestRouter_WorksWithNoOptionalDeps(t *testing.T) {
	e := &testEnv{
		auth: &stubAuth{}, wallet: &stubWallet{}, invoice: &stubInvoice{},
		payment: &stubPayment{}, refund: &stubRefund{}, admin: &stubAdmin{},
		users: &stubUserRepo{}, jwt: jwt.New(handlerTestSecret, time.Hour),
		pinger: &stubPinger{},
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	hs := &Handlers{
		Auth: NewAuthHandler(e.auth, nil), Wallet: NewWalletHandler(e.wallet),
		Invoice: NewInvoiceHandler(e.invoice, "/api/v1/pay"),
		Payment: NewPaymentHandler(e.payment, e.invoice, e.users),
		Refund:  NewRefundHandler(e.refund), Admin: NewAdminHandler(e.admin),
		Health: NewHealthHandler(e.pinger, discard),
	}
	e.router = NewRouter(hs, e.jwt, discard, RouterDeps{})

	if rec := e.do(t, http.MethodGet, "/healthz", "", nil); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want 200", rec.Code)
	}
	rec := e.do(t, http.MethodPost, "/api/v1/auth/login", "", loginBody("a@example.com"))
	if rec.Code != http.StatusOK {
		t.Errorf("login = %d, want 200 with no limiter configured", rec.Code)
	}
}
