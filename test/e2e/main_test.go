//go:build e2e

// Package e2e drives the running application over HTTP.
//
// It is the only test layer that exercises everything composed together: router,
// middleware chain, handlers, services, SQL, and a real Postgres — in one process tree,
// through the network. The layers below it each prove something narrower and say so
// explicitly (see agent_documentation/05-testing-strategy.md):
//
//	unit / service      business rules against in-memory mocks — cannot prove SQL is right
//	unit / handler      HTTP contract against stubbed services — cannot prove logic is right
//	integration / repo  real SQL, CAS, row locks — cannot prove the rules above them
//	e2e (this package)  all of it together, as a client actually sees it
//
// What that buys, concretely: a route registered at the wrong path, a middleware left
// off a group, a service wired to the wrong repository, or a DTO field renamed will all
// pass every layer below and fail here.
//
// What it still cannot prove: that any given behaviour is *correct in isolation*. A
// failure here says "the system misbehaves", not "this function is wrong". Diagnosis
// belongs to the narrower layers, which is why this suite complements them rather than
// replacing them.
package e2e

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

var (
	// baseURL is the running API. Set E2E_BASE_URL to point at it; without it the whole
	// suite is skipped, matching how internal/repository/integration_test.go treats
	// TEST_DATABASE_URL. That keeps `go test ./...` green on a machine with nothing running.
	baseURL string

	// adminToken is obtained once in TestMain rather than per test. The auth endpoints are
	// rate limited (agent_documentation/04-security.md section 10), and re-logging in for
	// every test would spend that budget on setup instead of on the behaviour under test.
	adminToken string
)

const (
	// readyTimeout bounds how long we wait for the stack to come up. `docker compose up -d`
	// returns before the API has finished booting, so a suite started right after it would
	// otherwise fail on connection-refused rather than on anything meaningful.
	readyTimeout = 60 * time.Second

	// rateLimitBudget caps how long a single request will keep retrying through 429s.
	// See client_test.go for why retrying is the right default here.
	rateLimitBudget = 90 * time.Second
)

func TestMain(m *testing.M) {
	baseURL = strings.TrimRight(os.Getenv("E2E_BASE_URL"), "/")
	if baseURL == "" {
		fmt.Fprintln(os.Stderr, "e2e: E2E_BASE_URL is not set — skipping the suite.")
		fmt.Fprintln(os.Stderr, "e2e: see test/e2e/README.md for how to run it.")
		os.Exit(0)
	}

	if err := waitForReady(baseURL); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %s never became ready: %v\n", baseURL, err)
		os.Exit(1)
	}

	tok, err := loginRaw(adminEmail(), adminPassword())
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: could not log in as admin (%s): %v\n", adminEmail(), err)
		fmt.Fprintln(os.Stderr, "e2e: the admin is seeded by `go run ./cmd/api migrate`;")
		fmt.Fprintln(os.Stderr, "e2e: set E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD if yours differ.")
		os.Exit(1)
	}
	adminToken = tok

	os.Exit(m.Run())
}

func adminEmail() string {
	if v := os.Getenv("E2E_ADMIN_EMAIL"); v != "" {
		return v
	}
	return "admin@example.com"
}

func adminPassword() string {
	if v := os.Getenv("E2E_ADMIN_PASSWORD"); v != "" {
		return v
	}
	return "admin12345"
}

// strictRateLimits reports whether the target is running with the shipped auth limits.
// The rate-limit scenario needs them; every other scenario is faster without them, so
// the two cannot be satisfied by one deployment. Rather than guess, the suite is told.
func strictRateLimits() bool {
	return os.Getenv("E2E_STRICT_RATELIMIT") == "1"
}

// waitForReady polls /readyz — not /healthz. Liveness answers "the process is up", which
// is true before the database connection is usable; readiness is the one that means the
// service can actually serve. Polling the wrong probe would let the suite start too early.
func waitForReady(base string) error {
	c := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(readyTimeout)

	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := c.Get(base + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("readyz returned %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return lastErr
}
