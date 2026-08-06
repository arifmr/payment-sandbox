package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// minJWTSecretLen matches the HS256 output size — a shorter key adds no security.
const minJWTSecretLen = 32

type Config struct {
	AppEnv      string
	HTTPPort    string
	DatabaseURL string

	JWTSecret     string
	JWTAccessTTL  time.Duration
	JWTRefreshTTL time.Duration

	BcryptCost int

	InvoiceExpiryCheckInterval time.Duration

	// Rate limiting on the auth endpoints. A rate of 0 disables that dimension.
	AuthRateLimitPerMinute  int
	AuthRateLimitBurst      int
	LoginRateLimitPerMinute int
	LoginRateLimitBurst     int

	// MetricsEnabled publishes /metrics. Default false: the endpoint reveals traffic
	// shape and route inventory, so it should be reachable by the scraper rather than
	// by the internet.
	MetricsEnabled bool
}

func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:                     getEnv("APP_ENV", "development"),
		HTTPPort:                   getEnv("HTTP_PORT", "8080"),
		DatabaseURL:                getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/payment_sandbox?sslmode=disable"),
		JWTSecret:                  getEnv("JWT_SECRET", ""),
		JWTAccessTTL:               getDurationEnv("JWT_ACCESS_TTL", 15*time.Minute),
		JWTRefreshTTL:              getDurationEnv("JWT_REFRESH_TTL", 7*24*time.Hour),
		BcryptCost:                 getIntEnv("BCRYPT_COST", 12),
		InvoiceExpiryCheckInterval: getDurationEnv("INVOICE_EXPIRY_CHECK_INTERVAL", 1*time.Minute),
		AuthRateLimitPerMinute:     getIntEnv("AUTH_RATE_LIMIT_PER_MINUTE", 30),
		AuthRateLimitBurst:         getIntEnv("AUTH_RATE_LIMIT_BURST", 10),
		LoginRateLimitPerMinute:    getIntEnv("LOGIN_RATE_LIMIT_PER_MINUTE", 5),
		LoginRateLimitBurst:        getIntEnv("LOGIN_RATE_LIMIT_BURST", 5),
		MetricsEnabled:             getBoolEnv("METRICS_ENABLED", false),
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	// HS256 keys shorter than the 256-bit hash output weaken the signature and are
	// usually a placeholder left in place by accident.
	if len(cfg.JWTSecret) < minJWTSecretLen {
		return nil, fmt.Errorf("JWT_SECRET must be at least %d characters", minJWTSecretLen)
	}
	if cfg.JWTAccessTTL <= 0 || cfg.JWTRefreshTTL <= 0 {
		return nil, fmt.Errorf("JWT_ACCESS_TTL and JWT_REFRESH_TTL must be positive durations")
	}
	if cfg.InvoiceExpiryCheckInterval <= 0 {
		return nil, fmt.Errorf("INVOICE_EXPIRY_CHECK_INTERVAL must be a positive duration")
	}
	return cfg, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getIntEnv(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// getBoolEnv accepts what strconv.ParseBool accepts (1/t/T/true/TRUE, 0/f/false, …).
// An unparseable value falls back to def rather than failing the boot, matching the
// other getters.
func getBoolEnv(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getDurationEnv(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
