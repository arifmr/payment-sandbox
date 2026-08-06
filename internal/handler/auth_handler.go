package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/dboarif/payment-sandbox/internal/middleware"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/ratelimit"
	"github.com/dboarif/payment-sandbox/internal/service"
)

type AuthHandler struct {
	auth service.AuthService
	// loginLimiter throttles login attempts per email address, complementing the
	// per-IP limiter on the route. The two close different attacks: per-IP stops one
	// host trying many accounts, per-email stops a distributed botnet grinding a
	// single account. May be nil, in which case no per-account limit applies.
	loginLimiter *ratelimit.Limiter
}

// NewAuthHandler wires the login limiter here rather than as middleware because the key
// is the email address, which only exists after the request body has been bound —
// middleware would have to read and rewind the body to see it.
func NewAuthHandler(a service.AuthService, loginLimiter *ratelimit.Limiter) *AuthHandler {
	return &AuthHandler{auth: a, loginLimiter: loginLimiter}
}

func toLoginResponse(p *service.TokenPair) model.LoginResponse {
	return model.LoginResponse{
		AccessToken:      p.AccessToken,
		AccessExpiresAt:  p.AccessExpiresAt,
		RefreshToken:     p.RefreshToken,
		RefreshExpiresAt: p.RefreshExpiresAt,
		User:             model.ToUser(p.User),
	}
}

// Register godoc
// @Summary      Register merchant
// @Description  Create a new merchant account. A wallet with zero balance is created automatically.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body model.RegisterRequest true "registration payload"
// @Success      201  {object} model.UserResponse
// @Failure      400  {object} model.ErrorResponse
// @Failure      409  {object} model.ErrorResponse
// @Router       /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req model.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	u, err := h.auth.Register(c.Request.Context(), req.Name, req.Email, req.Password)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, model.ToUser(u))
}

// Login godoc
// @Summary      Login
// @Description  Returns a short-lived access JWT and an opaque refresh token.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body model.LoginRequest true "credentials"
// @Success      200  {object} model.LoginResponse
// @Failure      400  {object} model.ErrorResponse
// @Failure      401  {object} model.ErrorResponse
// @Router       /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req model.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}

	// Normalised so "USER@x.com" and "user@x.com" share one bucket — otherwise case
	// variation alone would multiply an attacker's allowance.
	if h.loginLimiter != nil && h.loginLimiter.Enabled() {
		key := strings.ToLower(strings.TrimSpace(req.Email))
		if !h.loginLimiter.Allow(key) {
			middleware.RateLimited(c, h.loginLimiter, key)
			return
		}
	}

	pair, err := h.auth.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toLoginResponse(pair))
}

// Refresh godoc
// @Summary      Refresh tokens
// @Description  Rotates the refresh token: the supplied one is revoked, a new pair is returned.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body model.RefreshRequest true "refresh token"
// @Success      200  {object} model.LoginResponse
// @Failure      400  {object} model.ErrorResponse
// @Failure      401  {object} model.ErrorResponse
// @Router       /auth/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req model.RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	pair, err := h.auth.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toLoginResponse(pair))
}

// Logout godoc
// @Summary      Logout
// @Description  Revokes the supplied refresh token. Idempotent.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body model.LogoutRequest true "refresh token"
// @Success      204
// @Failure      400  {object} model.ErrorResponse
// @Router       /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	var req model.LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(badRequest(err))
		return
	}
	if err := h.auth.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
