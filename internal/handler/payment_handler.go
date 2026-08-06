package handler

import (
	"net/http"

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

type PaymentHandler struct {
	payment service.PaymentService
	invoice service.InvoiceService
	users   repository.UserRepository
}

func NewPaymentHandler(p service.PaymentService, inv service.InvoiceService, u repository.UserRepository) *PaymentHandler {
	return &PaymentHandler{payment: p, invoice: inv, users: u}
}

// GetPublicInvoice godoc
// @Summary      [Public] Get invoice for payment page
// @Tags         payment
// @Produce      json
// @Param        token path string true "payment token from invoice"
// @Success      200 {object} model.PublicInvoiceResponse
// @Failure      404 {object} model.ErrorResponse
// @Router       /pay/{token} [get]
func (h *PaymentHandler) GetPublicInvoice(c *gin.Context) {
	tok := c.Param("token")
	inv, err := h.invoice.GetByPaymentToken(c.Request.Context(), tok)
	if err != nil {
		c.Error(err)
		return
	}
	merchant, err := h.users.FindByID(c.Request.Context(), inv.MerchantID)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, model.ToPublicInvoice(inv, merchant.Name))
}

// CreateIntent godoc
// @Summary      [Public] Create payment intent
// @Description  Authentication is optional. If a logged-in user submits with method=WALLET, the payer's wallet is debited on SUCCESS.
// @Tags         payment
// @Accept       json
// @Produce      json
// @Param        token path string true "payment token from invoice"
// @Param        body  body model.CreatePaymentRequest true "payment method"
// @Success      201 {object} model.PaymentIntentResponse
// @Failure      400 {object} model.ErrorResponse
// @Failure      404 {object} model.ErrorResponse
// @Failure      422 {object} model.ErrorResponse
// @Router       /pay/{token} [post]
func (h *PaymentHandler) CreateIntent(c *gin.Context) {
	tok := c.Param("token")
	var req model.CreatePaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	var payer *uuid.UUID
	if uid, ok := middleware.UserID(c); ok {
		payer = &uid
	}
	pi, err := h.payment.CreateIntent(c.Request.Context(), tok, constant.PaymentMethod(req.Method), payer)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, model.ToPaymentIntent(pi))
}

// GetIntentByToken godoc
// @Summary      [Public] Payment status for an intent
// @Description  Lets the payer poll their payment status. The intent must belong to the invoice behind the token.
// @Tags         payment
// @Produce      json
// @Param        token path string true "payment token from invoice"
// @Param        id    path string true "payment intent id (uuid)"
// @Success      200 {object} model.PaymentIntentResponse
// @Failure      400 {object} model.ErrorResponse
// @Failure      404 {object} model.ErrorResponse
// @Router       /pay/{token}/intents/{id} [get]
func (h *PaymentHandler) GetIntentByToken(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid payment intent id"))
		return
	}
	pi, err := h.payment.GetIntentByToken(c.Request.Context(), c.Param("token"), id)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, model.ToPaymentIntent(pi))
}

// AdminGetIntent godoc
// @Summary      [Admin] Get a payment intent
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "payment intent id (uuid)"
// @Success      200 {object} model.PaymentIntentResponse
// @Failure      400 {object} model.ErrorResponse
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Failure      404 {object} model.ErrorResponse
// @Router       /admin/payments/{id} [get]
func (h *PaymentHandler) AdminGetIntent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid payment intent id"))
		return
	}
	pi, err := h.payment.GetIntent(c.Request.Context(), id)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, model.ToPaymentIntent(pi))
}

// AdminListIntents godoc
// @Summary      [Admin] Search payment intents
// @Description  Backs the admin payment simulation panel: find intents to finalize, filtered by invoice and/or status.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        invoice_id query string false "filter by invoice uuid"
// @Param        status     query string false "filter by status (PENDING/SUCCESS/FAILED)"
// @Param        page       query int    false "page (default 1)"
// @Param        page_size  query int    false "page size (default 20, max 100)"
// @Success      200 {object} pagination.Response{data=[]model.PaymentIntentResponse}
// @Failure      400 {object} model.ErrorResponse
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Router       /admin/payments [get]
func (h *PaymentHandler) AdminListIntents(c *gin.Context) {
	f := repository.PaymentIntentFilter{}
	if v := c.Query("invoice_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid invoice_id"))
			return
		}
		f.InvoiceID = &id
	}
	if v := c.Query("status"); v != "" {
		st := constant.PaymentIntentStatus(v)
		if !st.Valid() {
			c.Error(apperror.New(apperror.KindBadRequest, "INVALID_STATUS", "invalid status filter"))
			return
		}
		f.Status = &st
	}

	p := pagination.FromQuery(c)
	items, total, err := h.payment.ListIntents(c.Request.Context(), f, p.Offset(), p.Limit())
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]model.PaymentIntentResponse, 0, len(items))
	for i := range items {
		out = append(out, model.ToPaymentIntent(&items[i]))
	}
	c.JSON(http.StatusOK, pagination.Wrap(out, p, total))
}

// AdminProcess godoc
// @Summary      [Admin] Finalize a payment intent
// @Description  Transitions PENDING -> SUCCESS or FAILED. On SUCCESS, the invoice becomes PAID and wallet side-effects are applied atomically.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id   path string true "payment intent id (uuid)"
// @Param        body body model.PaymentActionRequest true "action"
// @Success      200 {object} model.PaymentIntentResponse
// @Failure      400 {object} model.ErrorResponse
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Failure      404 {object} model.ErrorResponse
// @Failure      422 {object} model.ErrorResponse
// @Router       /admin/payments/{id} [patch]
func (h *PaymentHandler) AdminProcess(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid payment intent id"))
		return
	}
	var req model.PaymentActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	pi, err := h.payment.Process(c.Request.Context(), id, req.Action == "SUCCESS")
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, model.ToPaymentIntent(pi))
}
