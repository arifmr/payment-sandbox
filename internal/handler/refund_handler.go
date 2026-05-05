package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/middleware"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/pkg/pagination"
	"github.com/dboarif/payment-sandbox/internal/service"
)

type RefundHandler struct {
	refund service.RefundService
}

func NewRefundHandler(r service.RefundService) *RefundHandler { return &RefundHandler{refund: r} }

// Request godoc
// @Summary      Request a refund
// @Description  Available only when the invoice is PAID. Initial state: REQUESTED.
// @Tags         refund
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body body model.RequestRefundRequest true "refund request"
// @Success      201 {object} model.RefundResponse
// @Failure      400 {object} model.ErrorResponse
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Failure      404 {object} model.ErrorResponse
// @Failure      422 {object} model.ErrorResponse
// @Router       /refunds [post]
func (h *RefundHandler) Request(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.Error(apperror.ErrUnauthorized)
		return
	}
	var req model.RequestRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	invID, err := uuid.Parse(req.InvoiceID)
	if err != nil {
		c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid invoice id"))
		return
	}
	rf, err := h.refund.Request(c.Request.Context(), uid, invID, req.Amount, req.Reason)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, model.ToRefund(rf))
}

// ListMine godoc
// @Summary      List own refunds
// @Tags         refund
// @Produce      json
// @Security     BearerAuth
// @Param        page      query int false "page (default 1)"
// @Param        page_size query int false "page size (default 20)"
// @Success      200 {object} pagination.Response{data=[]model.RefundResponse}
// @Failure      401 {object} model.ErrorResponse
// @Router       /refunds [get]
func (h *RefundHandler) ListMine(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.Error(apperror.ErrUnauthorized)
		return
	}
	p := pagination.FromQuery(c)
	items, total, err := h.refund.ListByMerchant(c.Request.Context(), uid, p.Offset(), p.Limit())
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]model.RefundResponse, 0, len(items))
	for i := range items {
		out = append(out, model.ToRefund(&items[i]))
	}
	c.JSON(http.StatusOK, pagination.Wrap(out, p, total))
}

// AdminList godoc
// @Summary      [Admin] List all refunds
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        page      query int false "page (default 1)"
// @Param        page_size query int false "page size (default 20)"
// @Success      200 {object} pagination.Response{data=[]model.RefundResponse}
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Router       /admin/refunds [get]
func (h *RefundHandler) AdminList(c *gin.Context) {
	p := pagination.FromQuery(c)
	items, total, err := h.refund.List(c.Request.Context(), p.Offset(), p.Limit())
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]model.RefundResponse, 0, len(items))
	for i := range items {
		out = append(out, model.ToRefund(&items[i]))
	}
	c.JSON(http.StatusOK, pagination.Wrap(out, p, total))
}

// AdminAction godoc
// @Summary      [Admin] Refund state machine action
// @Description  Single endpoint that drives the refund state machine via body action.
// @Description  REQUESTED -> APPROVE/REJECT, APPROVED -> PROCESS (SUCCESS, debits merchant wallet) / FAIL.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id   path string true "refund id (uuid)"
// @Param        body body model.RefundActionRequest true "action: APPROVE|REJECT|PROCESS|FAIL"
// @Success      200 {object} model.RefundResponse
// @Failure      400 {object} model.ErrorResponse
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Failure      404 {object} model.ErrorResponse
// @Failure      422 {object} model.ErrorResponse
// @Router       /admin/refunds/{id} [patch]
func (h *RefundHandler) AdminAction(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid refund id"))
		return
	}
	var req model.RefundActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	rf, err := h.refund.AdminAction(c.Request.Context(), id, service.RefundAction(req.Action))
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, model.ToRefund(rf))
}
