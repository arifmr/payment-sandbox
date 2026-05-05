package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/repository"
	"github.com/dboarif/payment-sandbox/internal/service"
)

type AdminHandler struct {
	admin service.AdminService
}

func NewAdminHandler(a service.AdminService) *AdminHandler { return &AdminHandler{admin: a} }

// Dashboard godoc
// @Summary      [Admin] Dashboard statistics
// @Description  Aggregates: total invoices, paid/failed/expired counts, total amount paid, total refund amount. Filterable by merchant and date range.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        merchant_id query string false "filter by merchant uuid"
// @Param        from        query string false "RFC3339 timestamp lower bound"
// @Param        to          query string false "RFC3339 timestamp upper bound"
// @Success      200 {object} repository.DashboardStats
// @Failure      400 {object} dto.ErrorResponse
// @Failure      401 {object} dto.ErrorResponse
// @Failure      403 {object} dto.ErrorResponse
// @Router       /admin/dashboard [get]
func (h *AdminHandler) Dashboard(c *gin.Context) {
	f := repository.DashboardFilter{}
	if v := c.Query("merchant_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid merchant_id"))
			return
		}
		f.MerchantID = &id
	}
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.Error(apperror.New(apperror.KindBadRequest, "INVALID_DATE", "invalid 'from' date"))
			return
		}
		f.From = &t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			c.Error(apperror.New(apperror.KindBadRequest, "INVALID_DATE", "invalid 'to' date"))
			return
		}
		f.To = &t
	}
	stats, err := h.admin.Dashboard(c.Request.Context(), f)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, stats)
}
