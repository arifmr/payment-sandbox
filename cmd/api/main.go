// Package main is the Payment Sandbox API entrypoint.
//
// @title           Payment Sandbox API
// @version         1.0
// @description     Payment Sandbox simulation API (invoice, payment intent, refund, wallet).
// @BasePath        /api/v1
// @schemes         http https
//
// @securityDefinitions.apikey BearerAuth
// @in   header
// @name Authorization
// @description Bearer access token. Format: "Bearer <token>"
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	_ "github.com/dboarif/payment-sandbox/docs" // swagger spec

	"github.com/dboarif/payment-sandbox/internal/config"
	"github.com/dboarif/payment-sandbox/internal/constant"
	"github.com/dboarif/payment-sandbox/internal/handler"
	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
	"github.com/dboarif/payment-sandbox/internal/pkg/hash"
	"github.com/dboarif/payment-sandbox/internal/pkg/jwt"
	"github.com/dboarif/payment-sandbox/internal/pkg/logger"
	"github.com/dboarif/payment-sandbox/internal/pkg/metrics"
	"github.com/dboarif/payment-sandbox/internal/pkg/ratelimit"
	"github.com/dboarif/payment-sandbox/internal/repository"
	"github.com/dboarif/payment-sandbox/internal/service"
	"github.com/dboarif/payment-sandbox/internal/transaction"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	log := logger.New(cfg.AppEnv)
	slog.SetDefault(log)

	db, err := repository.OpenDB(cfg.DatabaseURL)
	if err != nil {
		log.Error("db open failed", "err", err)
		os.Exit(1)
	}

	flag.Parse()
	args := flag.Args()
	hasher := hash.New(cfg.BcryptCost)

	if len(args) == 0 {
		args = append(args, "main")
	} else if args[0] != "main" && args[0] != "migrate" {
		args[0] = "main"
	}

	if args[0] == "migrate" {
		migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := repository.Migrate(migrateCtx, db); err != nil {
			migrateCancel()
			log.Error("migrate failed", "err", err)
			os.Exit(1)
		}
		migrateCancel()

		// Seed default admin (idempotent).
		if err := seedAdmin(db, hasher); err != nil {
			log.Warn("seed admin failed", "err", err)
			os.Exit(1)
		}

		os.Exit(0)
	}

	// Repos
	userRepo := repository.NewUserRepo(db)
	walletRepo := repository.NewWalletRepo(db)
	topupRepo := repository.NewTopupRepo(db)
	invRepo := repository.NewInvoiceRepo(db)
	piRepo := repository.NewPaymentIntentRepo(db)
	refundRepo := repository.NewRefundRepo(db)
	dashboardRepo := repository.NewDashboardRepo(db)
	refreshRepo := repository.NewRefreshTokenRepo(db)
	locker := repository.NewAdvisoryLocker(db)

	// Foundations
	uow := transaction.NewUoW(db)
	jwtMgr := jwt.New(cfg.JWTSecret, cfg.JWTAccessTTL)
	metricsReg := metrics.NewRegistry()

	// Rate limiters. Two dimensions on purpose: per-IP stops one host trying many
	// accounts, per-email stops a distributed attempt on a single account.
	authIPLimiter := ratelimit.New(cfg.AuthRateLimitPerMinute, time.Minute, cfg.AuthRateLimitBurst)
	loginLimiter := ratelimit.New(cfg.LoginRateLimitPerMinute, time.Minute, cfg.LoginRateLimitBurst)

	// Services
	authSvc := service.NewAuthService(userRepo, walletRepo, refreshRepo, hasher, jwtMgr, uow, cfg.JWTRefreshTTL)
	walletSvc := service.NewWalletService(topupRepo, walletRepo, uow)
	invSvc := service.NewInvoiceService(invRepo, piRepo, locker, uow)
	paySvc := service.NewPaymentService(invRepo, piRepo, walletRepo, uow)
	refundSvc := service.NewRefundService(refundRepo, invRepo, piRepo, walletRepo, uow)
	adminSvc := service.NewAdminService(dashboardRepo)

	// Background expiry sweeper.
	ctx, cancel := context.WithCancel(context.Background())
	go runExpirySweeper(ctx, log, invSvc, cfg.InvoiceExpiryCheckInterval)

	// Handlers
	hs := &handler.Handlers{
		Auth:    handler.NewAuthHandler(authSvc, loginLimiter),
		Wallet:  handler.NewWalletHandler(walletSvc),
		Invoice: handler.NewInvoiceHandler(invSvc, "/api/v1/pay"),
		Payment: handler.NewPaymentHandler(paySvc, invSvc, userRepo),
		Refund:  handler.NewRefundHandler(refundSvc),
		Admin:   handler.NewAdminHandler(adminSvc),
		Health:  handler.NewHealthHandler(db, log),
	}

	r := handler.NewRouter(hs, jwtMgr, log, handler.RouterDeps{
		AuthIPLimiter: authIPLimiter,
		Metrics:       metricsReg,
		ExposeMetrics: cfg.MetricsEnabled,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("http server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

func runExpirySweeper(ctx context.Context, log *slog.Logger, invSvc service.InvoiceService, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := invSvc.ExpireDue(ctx)
			if err != nil {
				log.Warn("expiry sweep failed", "err", err)
				continue
			}
			if res.Skipped {
				// Another replica holds the advisory lock. Expected in a multi-instance
				// deployment, so logged at debug rather than as a problem.
				log.Debug("expiry sweep skipped: another instance is sweeping")
				continue
			}
			if res.InvoicesExpired > 0 || res.IntentsFailed > 0 {
				log.Info("expiry sweep completed",
					"invoices_expired", res.InvoicesExpired,
					"intents_failed", res.IntentsFailed,
				)
			}
		}
	}
}

// seedAdmin creates a default ADMIN user if env ADMIN_EMAIL/ADMIN_PASSWORD are set
// and the user does not already exist.
func seedAdmin(db *sql.DB, hasher *hash.Hasher) error {
	email := os.Getenv("ADMIN_EMAIL")
	pwd := os.Getenv("ADMIN_PASSWORD")
	if email == "" || pwd == "" {
		return nil
	}
	if len(pwd) < 8 {
		return errors.New("ADMIN_PASSWORD must be at least 8 chars")
	}

	userRepo := repository.NewUserRepo(db)
	ctx := context.Background()
	existing, err := userRepo.FindByEmail(ctx, email)
	if err == nil && existing != nil {
		return nil
	}
	if err != nil && !errors.Is(err, apperror.ErrNotFound) {
		return err
	}
	hashed, err := hasher.Hash(pwd)
	if err != nil {
		return err
	}
	return userRepo.Create(ctx, &model.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: hashed,
		Name:         "Admin",
		Role:         constant.RoleAdmin,
	})
}
