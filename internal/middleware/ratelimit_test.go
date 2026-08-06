package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dboarif/payment-sandbox/internal/pkg/metrics"
	"github.com/dboarif/payment-sandbox/internal/pkg/ratelimit"
)

// serveLimited runs `calls` requests through RateLimitByIP from the given client address
// and returns each response.
func serveLimited(l *ratelimit.Limiter, clientIP string, calls int) []*httptest.ResponseRecorder {
	r := gin.New()
	r.Use(RateLimitByIP(l))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	out := make([]*httptest.ResponseRecorder, 0, calls)
	for i := 0; i < calls; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = clientIP + ":12345"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		out = append(out, rec)
	}
	return out
}

func TestRateLimitByIP_AllowsWithinBurstThenRejects(t *testing.T) {
	l := ratelimit.New(60, time.Minute, 2) // burst 2, 1/sec refill

	recs := serveLimited(l, "203.0.113.5", 3)

	if recs[0].Code != http.StatusOK || recs[1].Code != http.StatusOK {
		t.Fatalf("first two calls should pass, got %d and %d", recs[0].Code, recs[1].Code)
	}
	if recs[2].Code != http.StatusTooManyRequests {
		t.Fatalf("third call = %d, want 429", recs[2].Code)
	}
	if body := recs[2].Body.String(); !contains(body, "RATE_LIMITED") {
		t.Errorf("429 body should carry the RATE_LIMITED code: %s", body)
	}
}

// Retry-After lets a well-behaved client back off instead of hammering.
func TestRateLimitByIP_SetsRetryAfter(t *testing.T) {
	l := ratelimit.New(60, time.Minute, 1)

	recs := serveLimited(l, "203.0.113.6", 2)

	retry := recs[1].Header().Get("Retry-After")
	if retry == "" {
		t.Fatal("Retry-After is missing on the 429")
	}
	n, err := strconv.Atoi(retry)
	if err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive integer", retry)
	}
}

// Budgets are per address: one noisy client must not lock out everybody else.
func TestRateLimitByIP_KeysByClientAddress(t *testing.T) {
	l := ratelimit.New(60, time.Minute, 1)

	r := gin.New()
	r.Use(RateLimitByIP(l))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	call := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := call("198.51.100.1"); got != http.StatusOK {
		t.Fatalf("first client first call = %d", got)
	}
	if got := call("198.51.100.1"); got != http.StatusTooManyRequests {
		t.Fatalf("first client second call = %d, want 429", got)
	}
	if got := call("198.51.100.2"); got != http.StatusOK {
		t.Errorf("second client = %d, want 200 — budgets must be per address", got)
	}
}

// A nil or disabled limiter must install a pass-through, so the router does not have to
// branch on whether limiting is configured.
func TestRateLimitByIP_NilOrDisabledIsPassThrough(t *testing.T) {
	for name, l := range map[string]*ratelimit.Limiter{
		"nil":      nil,
		"disabled": ratelimit.New(0, time.Minute, 0),
	} {
		t.Run(name, func(t *testing.T) {
			for i, rec := range serveLimited(l, "203.0.113.7", 20) {
				if rec.Code != http.StatusOK {
					t.Fatalf("call %d = %d, want 200 with %s limiter", i+1, rec.Code, name)
				}
			}
		})
	}
}

// A rejected request must not reach the handler — otherwise the limit protects nothing.
func TestRateLimitByIP_ShortCircuitsTheHandler(t *testing.T) {
	l := ratelimit.New(60, time.Minute, 1)
	handlerCalls := 0

	r := gin.New()
	r.Use(RateLimitByIP(l))
	r.GET("/x", func(c *gin.Context) {
		handlerCalls++
		c.Status(http.StatusOK)
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "203.0.113.8:1234"
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	if handlerCalls != 1 {
		t.Errorf("handler ran %d times, want 1 — limited requests must be short-circuited", handlerCalls)
	}
}

// RateLimited is the exported helper for callers that discover the limit after body
// binding (the per-account login limit needs the email, known only then).
func TestRateLimited_WritesTheSameResponse(t *testing.T) {
	l := ratelimit.New(60, time.Minute, 1)
	l.Allow("account-key") // drain the bucket

	r := gin.New()
	r.GET("/x", func(c *gin.Context) { RateLimited(c, l, "account-key") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if !contains(rec.Body.String(), "RATE_LIMITED") {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After should be set here too")
	}
}

// ── metrics middleware ────────────────────────────────────────────────────────

func TestMetrics_ObservesRequests(t *testing.T) {
	reg := metrics.NewRegistry()

	r := gin.New()
	r.Use(Metrics(reg))
	r.GET("/items/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, id := range []string{"abc", "def"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items/"+id, nil))
	}

	snap := reg.Snapshot()
	if snap.Total != 2 {
		t.Fatalf("observed %d requests, want 2", snap.Total)
	}
	// Labelled by route pattern, not concrete path: one series per id would be
	// unbounded cardinality.
	out := reg.Render()
	if !contains(out, `route="/items/:id"`) {
		t.Errorf("expected the route pattern as label:\n%s", out)
	}
	for _, id := range []string{"abc", "def"} {
		if contains(out, `route="/items/`+id) {
			t.Errorf("concrete id %q leaked into a label", id)
		}
	}
}

func TestMetrics_RecordsStatusCode(t *testing.T) {
	reg := metrics.NewRegistry()

	r := gin.New()
	r.Use(Metrics(reg))
	r.GET("/fail", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/fail", nil))

	// Latency on the failure path is exactly what matters during an incident.
	if out := reg.Render(); !contains(out, `status="500"`) {
		t.Errorf("error responses must be measured:\n%s", out)
	}
}

func TestMetrics_UnmatchedRouteIsFolded(t *testing.T) {
	reg := metrics.NewRegistry()

	r := gin.New()
	r.Use(Metrics(reg))
	r.GET("/known", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/unknown-path", nil))

	if out := reg.Render(); !contains(out, `route="unmatched"`) {
		t.Errorf("unmatched requests should share one series:\n%s", out)
	}
}

// A nil registry must install a pass-through rather than panic.
func TestMetrics_NilRegistryIsPassThrough(t *testing.T) {
	r := gin.New()
	r.Use(Metrics(nil))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
