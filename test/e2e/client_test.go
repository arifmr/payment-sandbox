//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// client is a thin HTTP wrapper around the running API.
//
// It talks over the network on purpose. Calling handlers in-process would be faster but
// would skip exactly what this layer exists to check: that the route is registered where
// the docs say, that the middleware chain is attached to the group, and that the JSON on
// the wire matches the contract.
type client struct {
	token string
	http  *http.Client
}

func newClient() *client {
	return &client{http: &http.Client{Timeout: 30 * time.Second}}
}

// as returns a copy authenticated with the given bearer token. Copying rather than
// mutating means a test can hold an anonymous and an authenticated client side by side
// without one silently reconfiguring the other.
func (c *client) as(token string) *client {
	cp := *c
	cp.token = token
	return &cp
}

func adminClient() *client { return newClient().as(adminToken) }

type response struct {
	Status int
	Body   []byte
	Header http.Header
}

type reqCfg struct {
	noRetry bool
	headers map[string]string
}

type reqOpt func(*reqCfg)

// noRetryOn429 disables the rate-limit backoff for one request. Required by the
// rate-limit scenario, which asserts the 429 itself — with retrying on, that test would
// wait out the limiter and then see a 200, quietly proving nothing.
func noRetryOn429() reqOpt {
	return func(c *reqCfg) { c.noRetry = true }
}

func withHeader(k, v string) reqOpt {
	return func(c *reqCfg) {
		if c.headers == nil {
			c.headers = map[string]string{}
		}
		c.headers[k] = v
	}
}

func (c *client) get(t *testing.T, path string, opts ...reqOpt) *response {
	t.Helper()
	return c.do(t, http.MethodGet, path, nil, opts...)
}

func (c *client) post(t *testing.T, path string, body any, opts ...reqOpt) *response {
	t.Helper()
	return c.do(t, http.MethodPost, path, body, opts...)
}

func (c *client) patch(t *testing.T, path string, body any, opts ...reqOpt) *response {
	t.Helper()
	return c.do(t, http.MethodPatch, path, body, opts...)
}

// do issues the request, transparently waiting out 429s.
//
// The retry is not laziness about rate limiting — it is what makes the suite runnable
// against a stack using the shipped limits (30/min per IP on /auth/*). Every scenario
// registers its own merchant to stay isolated, and that costs two auth calls each, so a
// dozen scenarios exceed the per-IP budget on setup alone. Waiting is the honest fix:
// the limiter is doing its job, and the suite is not what it is defending against.
//
// Retrying is bounded by rateLimitBudget so a genuinely wedged limiter fails the test
// instead of hanging it, and honours Retry-After rather than guessing an interval.
func (c *client) do(t *testing.T, method, path string, body any, opts ...reqOpt) *response {
	t.Helper()

	cfg := reqCfg{}
	for _, o := range opts {
		o(&cfg)
	}

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("%s %s: marshal request body: %v", method, path, err)
		}
	}

	deadline := time.Now().Add(rateLimitBudget)
	for {
		req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("%s %s: build request: %v", method, path, err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		for k, v := range cfg.headers {
			req.Header.Set(k, v)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatalf("%s %s: read body: %v", method, path, readErr)
		}

		out := &response{Status: resp.StatusCode, Body: raw, Header: resp.Header}
		if out.Status != http.StatusTooManyRequests || cfg.noRetry {
			return out
		}
		wait := retryAfter(out)
		if time.Now().Add(wait).After(deadline) {
			t.Fatalf("%s %s: still rate limited after %s (Retry-After %s). "+
				"Relax AUTH_RATE_LIMIT_PER_MINUTE for the e2e run — see test/e2e/README.md",
				method, path, rateLimitBudget, wait)
		}
		time.Sleep(wait)
	}
}

// retryAfter reads the header the limiter sets, falling back to a second. A missing or
// unparseable value must not become a zero-length sleep, which would turn the backoff
// into a hot loop hammering the endpoint it is supposed to be backing off from.
func retryAfter(r *response) time.Duration {
	if v := r.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return time.Second
}

// ---------- assertions ----------

// requireStatus includes the body on failure. A bare "want 201, got 422" sends the reader
// back to the logs; the domain error code is almost always the actual answer.
func requireStatus(t *testing.T, r *response, want int) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status = %d, want %d\nbody: %s", r.Status, want, r.Body)
	}
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// requireErrorCode asserts the status *and* the domain code. Status alone is too coarse:
// several distinct failures share 422, and asserting only the number lets a test keep
// passing when the reason changes underneath it.
func requireErrorCode(t *testing.T, r *response, wantStatus int, wantCode string) {
	t.Helper()
	if r.Status != wantStatus {
		t.Fatalf("status = %d, want %d\nbody: %s", r.Status, wantStatus, r.Body)
	}
	var env errorEnvelope
	if err := json.Unmarshal(r.Body, &env); err != nil {
		t.Fatalf("error response is not the documented envelope: %v\nbody: %s", err, r.Body)
	}
	if env.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q\nbody: %s", env.Error.Code, wantCode, r.Body)
	}
}

func decode[T any](t *testing.T, r *response) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("decode %T: %v\nbody: %s", out, err, r.Body)
	}
	return out
}

// ---------- wire types ----------
//
// Declared here rather than imported from internal/model on purpose. These are the
// contract as a *client* sees it, so restating them means a renamed JSON tag breaks this
// suite loudly instead of being followed silently. It is the same reasoning the state
// machine tests use for transcribing the SRS diagrams rather than importing the graph
// (agent_documentation/05-testing-strategy.md section 7).

type userResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type loginResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	User         userResponse `json:"user"`
}

type invoiceResponse struct {
	ID            string `json:"id"`
	InvoiceNumber string `json:"invoice_number"`
	MerchantID    string `json:"merchant_id"`
	CustomerName  string `json:"customer_name"`
	CustomerEmail string `json:"customer_email"`
	Amount        int64  `json:"amount"`
	Status        string `json:"status"`
	PaymentToken  string `json:"payment_token"`
	PaymentLink   string `json:"payment_link"`
}

type publicInvoiceResponse struct {
	InvoiceNumber string `json:"invoice_number"`
	MerchantName  string `json:"merchant_name"`
	CustomerName  string `json:"customer_name"`
	Amount        int64  `json:"amount"`
	Status        string `json:"status"`
}

type intentResponse struct {
	ID        string `json:"id"`
	InvoiceID string `json:"invoice_id"`
	Method    string `json:"method"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
}

type walletResponse struct {
	MerchantID string `json:"merchant_id"`
	Balance    int64  `json:"balance"`
}

type refundResponse struct {
	ID        string `json:"id"`
	InvoiceID string `json:"invoice_id"`
	Amount    int64  `json:"amount"`
	Status    string `json:"status"`
}

type topupResponse struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
	Status string `json:"status"`
}

type paginated[T any] struct {
	Data       []T `json:"data"`
	Pagination struct {
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	} `json:"pagination"`
}

// loginRaw exists for TestMain, which has no *testing.T to fail through.
func loginRaw(email, password string) (string, error) {
	payload, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/login", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login returned %d: %s", resp.StatusCode, raw)
	}
	var out loginResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("login succeeded but returned no access token: %s", raw)
	}
	return out.AccessToken, nil
}
