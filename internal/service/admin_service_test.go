package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/repository"
)

// ── mockDashboardRepo ─────────────────────────────────────────────────────────

type mockDashboardRepo struct {
	stats          *repository.DashboardStats
	capturedFilter repository.DashboardFilter
}

func (m *mockDashboardRepo) Stats(_ context.Context, f repository.DashboardFilter) (*repository.DashboardStats, error) {
	m.capturedFilter = f
	return m.stats, nil
}

var _ repository.DashboardRepository = (*mockDashboardRepo)(nil)

// ── Dashboard ─────────────────────────────────────────────────────────────────

func TestAdminService_Dashboard_HappyPath(t *testing.T) {
	expected := &repository.DashboardStats{
		TotalInvoices:     10,
		TotalPaid:         6,
		TotalFailed:       2,
		TotalExpired:      1,
		TotalAmountPaid:   50000,
		TotalAmountRefund: 5000,
	}
	repo := &mockDashboardRepo{stats: expected}
	svc := NewAdminService(repo)

	stats, err := svc.Dashboard(context.Background(), repository.DashboardFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.TotalInvoices != expected.TotalInvoices {
		t.Fatalf("want TotalInvoices %d, got %d", expected.TotalInvoices, stats.TotalInvoices)
	}
	if stats.TotalPaid != expected.TotalPaid {
		t.Fatalf("want TotalPaid %d, got %d", expected.TotalPaid, stats.TotalPaid)
	}
	if stats.TotalAmountPaid != expected.TotalAmountPaid {
		t.Fatalf("want TotalAmountPaid %d, got %d", expected.TotalAmountPaid, stats.TotalAmountPaid)
	}
}

func TestAdminService_Dashboard_FilterPassedToRepo(t *testing.T) {
	merchantID := uuid.New()
	from := time.Now().Add(-24 * time.Hour)
	to := time.Now()

	repo := &mockDashboardRepo{stats: &repository.DashboardStats{}}
	svc := NewAdminService(repo)

	filter := repository.DashboardFilter{
		MerchantID: &merchantID,
		From:       &from,
		To:         &to,
	}

	_, err := svc.Dashboard(context.Background(), filter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.capturedFilter.MerchantID == nil || *repo.capturedFilter.MerchantID != merchantID {
		t.Fatalf("filter.MerchantID not forwarded correctly")
	}
	if repo.capturedFilter.From == nil || !repo.capturedFilter.From.Equal(from) {
		t.Fatalf("filter.From not forwarded correctly")
	}
	if repo.capturedFilter.To == nil || !repo.capturedFilter.To.Equal(to) {
		t.Fatalf("filter.To not forwarded correctly")
	}
}
