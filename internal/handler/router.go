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
	"github.com/dboarif/payment-sandbox/internal/pkg/metrics"
	"github.com/dboarif/payment-sandbox/internal/pkg/ratelimit"
)

type Handlers struct {
	Auth    *AuthHandler
	Wallet  *WalletHandler
	Invoice *InvoiceHandler
	Payment *PaymentHandler
	Refund  *RefundHandler
	Admin   *AdminHandler
	Health  *HealthHandler
}

// RouterDeps carries the optional infrastructure the router wires in. Each field may be
// nil, in which case that concern is simply not installed — keeping the router usable
// from tests that only care about routing.
type RouterDeps struct {
	// AuthIPLimiter throttles the unauthenticated auth endpoints per client address.
	AuthIPLimiter *ratelimit.Limiter
	// Metrics records request latency for the §5.1 target.
	Metrics *metrics.Registry
	// ExposeMetrics publishes /metrics. Off by default because the endpoint should be
	// reachable by the scraper, not by the internet.
	ExposeMetrics bool
}

func NewRouter(h *Handlers, j *jwt.Manager, log *slog.Logger, deps RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery(log))
	r.Use(middleware.Logger(log))
	// Metrics sits outside ErrorHandler so error responses are measured too — latency
	// on the failure path is exactly what you want to see during an incident.
	r.Use(middleware.Metrics(deps.Metrics))
	r.Use(middleware.ErrorHandler(log))

	// Liveness and readiness are deliberately separate endpoints; see health_handler.go.
	r.GET("/healthz", h.Health.Live)
	r.GET("/readyz", h.Health.Ready)
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	if deps.ExposeMetrics && deps.Metrics != nil {
		r.GET("/metrics", func(c *gin.Context) {
			c.String(http.StatusOK, deps.Metrics.Render())
		})
	}

	v1 := r.Group("/api/v1")

	// Auth. Rate limited per client address: these are the only endpoints an
	// unauthenticated caller can use to guess credentials.
	auth := v1.Group("/auth")
	auth.Use(middleware.RateLimitByIP(deps.AuthIPLimiter))
	auth.POST("/register", h.Auth.Register)
	auth.POST("/login", h.Auth.Login)
	auth.POST("/refresh", h.Auth.Refresh)
	auth.POST("/logout", h.Auth.Logout)

	// Public payment endpoints (auth optional — payer may be logged-in or not).
	pay := v1.Group("/pay")
	pay.Use(middleware.Auth(j, true)) // optional
	pay.GET("/:token", h.Payment.GetPublicInvoice)
	pay.POST("/:token", h.Payment.CreateIntent)
	pay.GET("/:token/intents/:id", h.Payment.GetIntentByToken)

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
		admin.GET("/payments", h.Payment.AdminListIntents)
		admin.GET("/payments/:id", h.Payment.AdminGetIntent)
		admin.PATCH("/payments/:id", h.Payment.AdminProcess)
		admin.GET("/refunds", h.Refund.AdminList)
		admin.PATCH("/refunds/:id", h.Refund.AdminAction)
		admin.GET("/dashboard", h.Admin.Dashboard)
	}

	return r
}
