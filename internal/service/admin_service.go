package service

import (
	"context"

	"github.com/dboarif/payment-sandbox/internal/repository"
)

type AdminService interface {
	Dashboard(ctx context.Context, f repository.DashboardFilter) (*repository.DashboardStats, error)
}

type adminService struct {
	dashboard repository.DashboardRepository
}

func NewAdminService(d repository.DashboardRepository) AdminService {
	return &adminService{dashboard: d}
}

func (s *adminService) Dashboard(ctx context.Context, f repository.DashboardFilter) (*repository.DashboardStats, error) {
	return s.dashboard.Stats(ctx, f)
}
