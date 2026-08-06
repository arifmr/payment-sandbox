//go:build e2e

package e2e

import (
	"net/http"
	"strconv"
	"testing"
)

// TestE2E_RateLimit_AuthEndpointsRejectABurst is gated behind E2E_STRICT_RATELIMIT=1.
//
// The gate is not squeamishness — it is a genuine conflict. Every other scenario registers
// its own merchant, which costs auth calls, so the suite is normally run against a stack
// with relaxed limits (see test/e2e/README.md). Asserting the limiter needs the shipped
// values. One deployment cannot satisfy both, so the suite is told which it is talking to
// rather than guessing from timing.
//
// Requests here opt out of the client's 429 backoff: with retrying on, the test would
// quietly wait out the limiter and then observe a 200, proving nothing at all.
func TestE2E_RateLimit_AuthEndpointsRejectABurst(t *testing.T) {
	if !strictRateLimits() {
		t.Skip("needs the shipped auth rate limits; set E2E_STRICT_RATELIMIT=1 and run against a default-configured stack")
	}

	email := uniqueEmail("ratelimit")
	anon := newClient()

	// Per-account limiting is the tighter of the two dimensions (5/min, burst 5), so a
	// single account is enough to trip it well before the per-IP budget.
	var limited *response
	const attempts = 30
	for i := 0; i < attempts; i++ {
		resp := anon.post(t, "/api/v1/auth/login",
			map[string]any{"email": email, "password": "wrong-on-purpose"},
			noRetryOn429())

		if resp.Status == http.StatusTooManyRequests {
			limited = resp
			break
		}
		if resp.Status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401 before the limit or 429 at it\nbody: %s",
				i+1, resp.Status, resp.Body)
		}
	}

	if limited == nil {
		t.Fatalf("%d failed logins in a row were all accepted; the auth limiter is not enforcing", attempts)
	}

	requireErrorCode(t, limited, http.StatusTooManyRequests, "RATE_LIMITED")

	// Retry-After is what lets a well-behaved client back off instead of hammering. Without
	// it the only strategy available to a client is to keep trying.
	retry := limited.Header.Get("Retry-After")
	if retry == "" {
		t.Error("429 carried no Retry-After header")
	} else if secs, err := strconv.Atoi(retry); err != nil || secs <= 0 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", retry)
	}
}

// TestE2E_RateLimit_HealthProbesAreNotLimited matters operationally: an orchestrator polls
// liveness and readiness continuously, and a 429 there reads as an outage. The probes are
// deliberately outside the limiter's scope.
func TestE2E_RateLimit_HealthProbesAreNotLimited(t *testing.T) {
	c := newClient()

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			for i := 0; i < 60; i++ {
				resp := c.get(t, path, noRetryOn429())
				if resp.Status == http.StatusTooManyRequests {
					t.Fatalf("%s was rate limited after %d probes; an orchestrator would read this as a failing instance",
						path, i+1)
				}
				if resp.Status != http.StatusOK {
					t.Fatalf("%s returned %d\nbody: %s", path, resp.Status, resp.Body)
				}
			}
		})
	}
}

// TestE2E_RateLimit_BusinessEndpointsAreNotLimited checks the limiter's scope is confined
// to /auth/*. Throttling an authenticated merchant's own dashboard would be an outage of
// our own making, and those endpoints are already gated by a token.
func TestE2E_RateLimit_BusinessEndpointsAreNotLimited(t *testing.T) {
	m := newMerchant(t)

	for i := 0; i < 60; i++ {
		resp := m.get(t, "/api/v1/wallet", noRetryOn429())
		if resp.Status == http.StatusTooManyRequests {
			t.Fatalf("an authenticated business endpoint was rate limited after %d calls", i+1)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("GET /wallet returned %d\nbody: %s", resp.Status, resp.Body)
		}
	}
}
