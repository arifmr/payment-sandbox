package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/repository"
)

// errDatabaseDown stands in for any unclassified infrastructure failure.
var errDatabaseDown = errors.New("dial tcp 127.0.0.1:5432: connection refused")

// SRS §2.6: the dashboard exposes every required aggregate.
func TestDashboard_ReturnsAllRequiredMetrics(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodGet, "/api/v1/admin/dashboard", bearer, nil)

	assertStatus(t, rec, http.StatusOK)

	var body map[string]any
	decode(t, rec, &body)
	required := []string{
		"total_invoices", "total_paid", "total_failed", "total_expired",
		"total_amount_paid", "total_amount_refund",
	}
	for _, key := range required {
		if _, present := body[key]; !present {
			t.Errorf("dashboard response is missing %q", key)
		}
	}

	var stats repository.DashboardStats
	decode(t, rec, &stats)
	if stats.TotalInvoices != 10 || stats.TotalPaid != 6 || stats.TotalFailed != 2 || stats.TotalExpired != 1 {
		t.Errorf("counts not passed through: %+v", stats)
	}
	if stats.TotalAmountPaid != 60000 || stats.TotalAmountRefund != 5000 {
		t.Errorf("amounts not passed through: %+v", stats)
	}
}

// SRS §2.6: filterable by merchant and date range.
func TestDashboard_ForwardsFilters(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	merchantID := uuid.New()
	from := "2026-01-01T00:00:00Z"
	to := "2026-06-30T23:59:59Z"

	rec := e.do(t, http.MethodGet,
		"/api/v1/admin/dashboard?merchant_id="+merchantID.String()+"&from="+from+"&to="+to, bearer, nil)

	assertStatus(t, rec, http.StatusOK)

	f := e.admin.lastFilter
	if f.MerchantID == nil || *f.MerchantID != merchantID {
		t.Errorf("merchant filter = %v, want %s", f.MerchantID, merchantID)
	}
	if f.From == nil || f.From.Format(time.RFC3339) != from {
		t.Errorf("from = %v, want %s", f.From, from)
	}
	if f.To == nil || f.To.Format(time.RFC3339) != to {
		t.Errorf("to = %v, want %s", f.To, to)
	}
}

func TestDashboard_NoFiltersMeansGlobal(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)

	rec := e.do(t, http.MethodGet, "/api/v1/admin/dashboard", bearer, nil)

	assertStatus(t, rec, http.StatusOK)
	f := e.admin.lastFilter
	if f.MerchantID != nil || f.From != nil || f.To != nil {
		t.Errorf("expected an empty filter, got %+v", f)
	}
}

func TestDashboard_RejectsInvalidFilters(t *testing.T) {
	cases := []struct{ name, query, wantCode string }{
		{"bad merchant id", "merchant_id=not-a-uuid", "INVALID_ID"},
		{"bad from", "from=yesterday", "INVALID_DATE"},
		{"bad to", "to=2026-99-99", "INVALID_DATE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv()
			_, bearer := e.tokenFor(t, constant.RoleAdmin)

			rec := e.do(t, http.MethodGet, "/api/v1/admin/dashboard?"+tc.query, bearer, nil)

			assertStatus(t, rec, http.StatusBadRequest)
			if code := errCode(t, rec); code != tc.wantCode {
				t.Errorf("error code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

// SRS §2.6 is an admin capability: a merchant must not see cross-merchant totals.
func TestDashboard_MerchantIsForbidden(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleMerchant)

	rec := e.do(t, http.MethodGet, "/api/v1/admin/dashboard", bearer, nil)

	assertStatus(t, rec, http.StatusForbidden)
	if e.admin.lastFilter.MerchantID != nil {
		t.Fatal("the dashboard service must not be reached by a merchant")
	}
}

func TestDashboard_RequiresAuth(t *testing.T) {
	e := newTestEnv()
	rec := e.do(t, http.MethodGet, "/api/v1/admin/dashboard", "", nil)
	assertStatus(t, rec, http.StatusUnauthorized)
}

func TestDashboard_ServiceErrorBecomes500WithoutDetail(t *testing.T) {
	e := newTestEnv()
	_, bearer := e.tokenFor(t, constant.RoleAdmin)
	e.admin.statsFn = func(context.Context, repository.DashboardFilter) (*repository.DashboardStats, error) {
		return nil, errDatabaseDown
	}

	rec := e.do(t, http.MethodGet, "/api/v1/admin/dashboard", bearer, nil)

	assertStatus(t, rec, http.StatusInternalServerError)
	if code := errCode(t, rec); code != "INTERNAL" {
		t.Errorf("error code = %q, want INTERNAL", code)
	}
	if body := rec.Body.String(); strings.Contains(body, "connection refused") {
		t.Errorf("internal detail leaked: %s", body)
	}
}
