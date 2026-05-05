package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/middleware"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
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
