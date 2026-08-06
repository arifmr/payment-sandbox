package handler

import (
	"net/http"
	"testing"
)

// The merchant and admin groups are mounted on a "/" sub-group, which is exactly
// the kind of path-joining that silently produces "/api/v1//wallet". This test
// pins every documented route to the path the README and Swagger advertise.
func TestRouter_AllDocumentedRoutesAreRegistered(t *testing.T) {
	e := newTestEnv()

	registered := map[string]bool{}
	for _, r := range e.router.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	want := []string{
		"GET /healthz",
		"GET /readyz",

		"POST /api/v1/auth/register",
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/refresh",
		"POST /api/v1/auth/logout",

		"GET /api/v1/pay/:token",
		"POST /api/v1/pay/:token",
		"GET /api/v1/pay/:token/intents/:id",

		"GET /api/v1/wallet",
		"POST /api/v1/wallet/topup",
		"GET /api/v1/wallet/topups",

		"POST /api/v1/invoices",
		"GET /api/v1/invoices",
		"GET /api/v1/invoices/:id",

		"POST /api/v1/refunds",
		"GET /api/v1/refunds",

		"GET /api/v1/admin/topups",
		"PATCH /api/v1/admin/topups/:id",
		"GET /api/v1/admin/payments",
		"GET /api/v1/admin/payments/:id",
		"PATCH /api/v1/admin/payments/:id",
		"GET /api/v1/admin/refunds",
		"PATCH /api/v1/admin/refunds/:id",
		"GET /api/v1/admin/dashboard",
	}

	for _, route := range want {
		if !registered[route] {
			t.Errorf("route %q is not registered", route)
		}
	}
}

// No route may end up with a doubled slash from group composition.
func TestRouter_NoDoubledSlashesInPaths(t *testing.T) {
	e := newTestEnv()
	for _, r := range e.router.Routes() {
		for i := 1; i < len(r.Path); i++ {
			if r.Path[i] == '/' && r.Path[i-1] == '/' {
				t.Errorf("route %s %s contains a doubled slash", r.Method, r.Path)
			}
		}
	}
}

// SRS §5.4: Swagger UI must be served so the API documentation is reachable.
func TestRouter_SwaggerUIIsMounted(t *testing.T) {
	e := newTestEnv()

	rec := e.do(t, http.MethodGet, "/swagger/index.html", "", nil)
	if rec.Code == http.StatusNotFound {
		t.Fatal("/swagger/index.html is not served")
	}
}

// Every request — including failures — should carry a correlation id.
func TestRouter_RequestIDOnEveryResponse(t *testing.T) {
	e := newTestEnv()

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/api/v1/wallet"},           // 401
		{http.MethodGet, "/api/v1/no-such-endpoint"}, // 404
	} {
		rec := e.do(t, tc.method, tc.path, "", nil)
		if rec.Header().Get("X-Request-ID") == "" {
			t.Errorf("%s %s: response has no X-Request-ID (status %d)", tc.method, tc.path, rec.Code)
		}
	}
}

func TestRouter_UnknownPathIs404(t *testing.T) {
	e := newTestEnv()
	rec := e.do(t, http.MethodGet, "/api/v1/does-not-exist", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Merchant-scoped routes must reject an admin token, and vice versa. This table
// is the single place the whole role matrix from SRS §2.1 is asserted.
func TestRouter_RoleMatrix(t *testing.T) {
	type expectation struct {
		method, path string
		merchantOK   bool
		adminOK      bool
	}
	body := map[string]any{"action": "SUCCESS"}

	cases := []expectation{
		{http.MethodGet, "/api/v1/wallet", true, false},
		{http.MethodGet, "/api/v1/wallet/topups", true, false},
		{http.MethodGet, "/api/v1/invoices", true, false},
		{http.MethodGet, "/api/v1/refunds", true, false},
		{http.MethodGet, "/api/v1/admin/dashboard", false, true},
		{http.MethodGet, "/api/v1/admin/refunds", false, true},
		{http.MethodGet, "/api/v1/admin/topups", false, true},
		{http.MethodGet, "/api/v1/admin/payments", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			e := newTestEnv()

			_, merchantToken := e.tokenFor(t, "MERCHANT")
			rec := e.do(t, tc.method, tc.path, merchantToken, body)
			if tc.merchantOK && rec.Code == http.StatusForbidden {
				t.Errorf("merchant was forbidden but should be allowed")
			}
			if !tc.merchantOK && rec.Code != http.StatusForbidden {
				t.Errorf("merchant got %d, want 403", rec.Code)
			}

			_, adminToken := e.tokenFor(t, "ADMIN")
			rec = e.do(t, tc.method, tc.path, adminToken, body)
			if tc.adminOK && rec.Code == http.StatusForbidden {
				t.Errorf("admin was forbidden but should be allowed")
			}
			if !tc.adminOK && rec.Code != http.StatusForbidden {
				t.Errorf("admin got %d, want 403", rec.Code)
			}
		})
	}
}
