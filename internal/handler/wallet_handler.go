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

type WalletHandler struct {
	wallet service.WalletService
}

func NewWalletHandler(w service.WalletService) *WalletHandler { return &WalletHandler{wallet: w} }

// Get godoc
// @Summary      Get wallet balance
// @Tags         wallet
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object} model.WalletResponse
// @Failure      401  {object} model.ErrorResponse
// @Router       /wallet [get]
func (h *WalletHandler) Get(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.Error(apperror.ErrUnauthorized)
		return
	}
	w, err := h.wallet.GetBalance(c.Request.Context(), uid)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, model.ToWallet(w))
}

// RequestTopup godoc
// @Summary      Request a wallet top-up
// @Description  Creates a top-up record with status PENDING. An admin must process it to credit the wallet.
// @Tags         wallet
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body body model.TopupRequest true "top-up request"
// @Success      201  {object} model.TopupResponse
// @Failure      400  {object} model.ErrorResponse
// @Failure      401  {object} model.ErrorResponse
// @Router       /wallet/topup [post]
func (h *WalletHandler) RequestTopup(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.Error(apperror.ErrUnauthorized)
		return
	}
	var req model.TopupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	t, err := h.wallet.RequestTopup(c.Request.Context(), uid, req.Amount)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, model.ToTopup(t))
}

// ListMyTopups godoc
// @Summary      List own top-ups
// @Tags         wallet
// @Produce      json
// @Security     BearerAuth
// @Param        page      query int false "page (default 1)"
// @Param        page_size query int false "page size (default 20, max 100)"
// @Success      200 {object} pagination.Response{data=[]model.TopupResponse}
// @Failure      401 {object} model.ErrorResponse
// @Router       /wallet/topups [get]
func (h *WalletHandler) ListMyTopups(c *gin.Context) {
	uid, ok := middleware.UserID(c)
	if !ok {
		c.Error(apperror.ErrUnauthorized)
		return
	}
	p := pagination.FromQuery(c)
	items, total, err := h.wallet.ListTopups(c.Request.Context(), &uid, p.Offset(), p.Limit())
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]model.TopupResponse, 0, len(items))
	for i := range items {
		out = append(out, model.ToTopup(&items[i]))
	}
	c.JSON(http.StatusOK, pagination.Wrap(out, p, total))
}

// AdminListTopups godoc
// @Summary      [Admin] List all top-ups
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        merchant_id query string false "filter by merchant uuid"
// @Param        page        query int    false "page (default 1)"
// @Param        page_size   query int    false "page size (default 20)"
// @Success      200 {object} pagination.Response{data=[]model.TopupResponse}
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Router       /admin/topups [get]
func (h *WalletHandler) AdminListTopups(c *gin.Context) {
	p := pagination.FromQuery(c)
	var merchantID *uuid.UUID
	if v := c.Query("merchant_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid merchant_id"))
			return
		}
		merchantID = &id
	}
	items, total, err := h.wallet.ListTopups(c.Request.Context(), merchantID, p.Offset(), p.Limit())
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]model.TopupResponse, 0, len(items))
	for i := range items {
		out = append(out, model.ToTopup(&items[i]))
	}
	c.JSON(http.StatusOK, pagination.Wrap(out, p, total))
}

// AdminProcessTopup godoc
// @Summary      [Admin] Finalize a top-up
// @Description  Transitions PENDING -> SUCCESS or FAILED. On SUCCESS, credits the merchant wallet atomically.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path string true "topup id (uuid)"
// @Param        body  body model.TopupActionRequest true "action"
// @Success      200 {object} model.TopupResponse
// @Failure      400 {object} model.ErrorResponse
// @Failure      401 {object} model.ErrorResponse
// @Failure      403 {object} model.ErrorResponse
// @Failure      404 {object} model.ErrorResponse
// @Failure      422 {object} model.ErrorResponse
// @Router       /admin/topups/{id} [patch]
func (h *WalletHandler) AdminProcessTopup(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.New(apperror.KindBadRequest, "INVALID_ID", "invalid topup id"))
		return
	}
	var req model.TopupActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	t, err := h.wallet.ProcessTopup(c.Request.Context(), id, req.Action == "SUCCESS")
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, model.ToTopup(t))
}
