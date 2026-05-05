package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/middleware"
	"github.com/dboarif/payment-sandbox/internal/pkg/jwt"
)

type Handlers struct {
	Auth    *AuthHandler
	Wallet  *WalletHandler
	Invoice *InvoiceHandler
	Payment *PaymentHandler
	Refund  *RefundHandler
	Admin   *AdminHandler
}

func NewRouter(h *Handlers, j *jwt.Manager, log *slog.Logger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery(log))
	r.Use(middleware.Logger(log))
	r.Use(middleware.ErrorHandler(log))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	v1 := r.Group("/api/v1")

	// Auth
	auth := v1.Group("/auth")
	auth.POST("/register", h.Auth.Register)
	auth.POST("/login", h.Auth.Login)
	auth.POST("/refresh", h.Auth.Refresh)
	auth.POST("/logout", h.Auth.Logout)

	// Public payment endpoints (auth optional — payer may be logged-in or not).
	pay := v1.Group("/pay")
	pay.Use(middleware.Auth(j, true)) // optional
	pay.GET("/:token", h.Payment.GetPublicInvoice)
	pay.POST("/:token", h.Payment.CreateIntent)

	// Authenticated routes
	authed := v1.Group("/")
	authed.Use(middleware.Auth(j, false))
	{
		// Merchant
		merchant := authed.Group("/")
		merchant.Use(middleware.RequireRole(string(constant.RoleMerchant)))
		merchant.GET("/wallet", h.Wallet.Get)
		merchant.POST("/wallet/topup", h.Wallet.RequestTopup)
		merchant.GET("/wallet/topups", h.Wallet.ListMyTopups)

		merchant.POST("/invoices", h.Invoice.Create)
		merchant.GET("/invoices", h.Invoice.List)
		merchant.GET("/invoices/:id", h.Invoice.GetByID)

		merchant.POST("/refunds", h.Refund.Request)
		merchant.GET("/refunds", h.Refund.ListMine)

		// Admin
		admin := authed.Group("/admin")
		admin.Use(middleware.RequireRole(string(constant.RoleAdmin)))
		admin.GET("/topups", h.Wallet.AdminListTopups)
		admin.PATCH("/topups/:id", h.Wallet.AdminProcessTopup)
		admin.PATCH("/payments/:id", h.Payment.AdminProcess)
		admin.GET("/refunds", h.Refund.AdminList)
		admin.PATCH("/refunds/:id", h.Refund.AdminAction)
		admin.GET("/dashboard", h.Admin.Dashboard)
	}

	return r
}
