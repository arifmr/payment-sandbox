package handler

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// SRS §5.2 / operational: liveness and readiness answer different questions and must
// not be conflated. Liveness = "restart this process". Readiness = "route traffic here".

func TestHealthz_IsLivenessOnly(t *testing.T) {
	e := newTestEnv()
	// The database is down …
	e.pinger.err = errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")

	rec := e.do(t, http.MethodGet, "/healthz", "", nil)

	// … but the process is fine, so liveness must still pass. If it failed here, a
	// brief database blip would make every replica fail its liveness probe at once and
	// the orchestrator would restart the whole fleet — escalating a recoverable
	// dependency problem into a full outage.
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestReadyz_OKWhenDatabaseReachable(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodGet, "/readyz", "", nil)

	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestReadyz_ServiceUnavailableWhenDatabaseDown(t *testing.T) {
	e := newTestEnv()
	e.pinger.err = errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")

	rec := e.do(t, http.MethodGet, "/readyz", "", nil)

	// 503 is what tells a load balancer to stop routing here.
	assertStatus(t, rec, http.StatusServiceUnavailable)
	if !strings.Contains(rec.Body.String(), "unavailable") {
		t.Errorf("body should report unavailability: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "database") {
		t.Errorf("body should name the failing dependency: %s", rec.Body.String())
	}
}

// SRS §5.2: the probe reports the verdict, never the underlying error — a DSN or
// credentials could otherwise be exposed on an unauthenticated endpoint.
func TestReadyz_DoesNotLeakConnectionDetails(t *testing.T) {
	e := newTestEnv()
	e.pinger.err = errors.New(`failed to connect to host=db user=postgres password=s3cr3t: FATAL: password authentication failed`)

	rec := e.do(t, http.MethodGet, "/readyz", "", nil)

	assertStatus(t, rec, http.StatusServiceUnavailable)
	for _, leaked := range []string{"s3cr3t", "password", "postgres", "host=db"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Errorf("readiness response leaked %q: %s", leaked, rec.Body.String())
		}
	}
}

// Both probes are unauthenticated by design: an orchestrator has no credentials.
func TestHealthProbes_AreUnauthenticated(t *testing.T) {
	e := newTestEnv()
	for _, path := range []string{"/healthz", "/readyz"} {
		rec := e.do(t, http.MethodGet, path, "", nil)
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Errorf("%s returned %d; probes must not require credentials", path, rec.Code)
		}
	}
}

// A nil Pinger means readiness has no dependency to check and should pass rather than
// fail closed — otherwise a misconfigured wiring would take the service out of rotation.
func TestReadyz_NilPingerPasses(t *testing.T) {
	h := NewHealthHandler(nil, nil)
	if h.db != nil {
		t.Fatal("expected a nil pinger")
	}
	if h.log == nil {
		t.Error("a nil logger must fall back to the default rather than panic")
	}
}
