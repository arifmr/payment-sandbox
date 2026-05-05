package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv      string
	HTTPPort    string
	DatabaseURL string

	JWTSecret     string
	JWTAccessTTL  time.Duration
	JWTRefreshTTL time.Duration

	BcryptCost int

	InvoiceExpiryCheckInterval time.Duration
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
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
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

func getDurationEnv(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
