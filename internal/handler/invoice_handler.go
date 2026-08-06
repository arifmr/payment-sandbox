package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/middleware"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/pkg/pagination"
	"github.com/dboarif/payment-sandbox/internal/repository"
	"github.com/dboarif/payment-sandbox/internal/service"
)

type InvoiceHandler struct {
	invoice         service.InvoiceService
	paymentLinkBase string
}

func NewInvoiceHandler(i service.InvoiceService, paymentLinkBase string) *InvoiceHandler {
	return &InvoiceHandler{invoice: i, paymentLinkBase: paymentLinkBase}
}

// Create godoc
// @Summary      Create invoice
// @Tags         invoice
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body body model.CreateInvoiceRequest true "invoice payload"
// @Success      201  {object} model.InvoiceResponse
// @Failure      400  {object} model.ErrorResponse
// @Failure      401  {object} model.ErrorResponse
// @Router       /invoices [post]
func (h *InvoiceHandler) Create(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.Error(apperror.ErrUnauthorized)
		return
	}
	var req model.CreateInvoiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	inv, err := h.invoice.Create(c.Request.Context(), service.CreateInvoiceInput{
		MerchantID:    uid,
		CustomerName:  req.CustomerName,
		CustomerEmail: req.CustomerEmail,
		Description:   req.Description,
		Amount:        req.Amount,
		DueDate:       req.DueDate,
	})
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, model.ToInvoice(inv, h.paymentLinkBase))
}

// GetByID godoc
// @Summary      Get invoice by id (own only)
// @Tags         invoice
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "invoice id (uuid)"
// @Success      200 {object} model.InvoiceResponse
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Failure      404 {object} model.ErrorResponse
// @Router       /invoices/{id} [get]
func (h *InvoiceHandler) GetByID(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.Error(apperror.ErrUnauthorized)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid invoice id"))
		return
	}
	inv, err := h.invoice.GetByID(c.Request.Context(), id, uid)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, model.ToInvoice(inv, h.paymentLinkBase))
}

// List godoc
// @Summary      List own invoices
// @Tags         invoice
// @Produce      json
// @Security     BearerAuth
// @Param        status    query string false "filter by status (PENDING/PAID/EXPIRED)"
// @Param        from      query string false "RFC3339 timestamp lower bound on created_at"
// @Param        to        query string false "RFC3339 timestamp upper bound on created_at"
// @Param        page      query int    false "page (default 1)"
// @Param        page_size query int    false "page size (default 20)"
// @Success      200 {object} pagination.Response{data=[]model.InvoiceResponse}
// @Failure      400 {object} model.ErrorResponse
// @Failure      401 {object} model.ErrorResponse
// @Router       /invoices [get]
func (h *InvoiceHandler) List(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.Error(apperror.ErrUnauthorized)
		return
	}
	p := pagination.FromQuery(c)
	f := repository.InvoiceFilter{MerchantID: &uid}
	if s := c.Query("status"); s != "" {
		st := constant.InvoiceStatus(s)
		if !st.Valid() {
			c.Error(apperror.New(apperror.KindBadRequest, "INVALID_STATUS", "invalid status filter"))
			return
		}
		f.Status = &st
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

	items, total, err := h.invoice.List(c.Request.Context(), f, p.Offset(), p.Limit())
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]model.InvoiceResponse, 0, len(items))
	for i := range items {
		out = append(out, model.ToInvoice(&items[i], h.paymentLinkBase))
	}
	c.JSON(http.StatusOK, pagination.Wrap(out, p, total))
}
