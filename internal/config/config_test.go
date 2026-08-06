package config

import (
	"strings"
	"testing"
	"time"
)

const validSecret = "0123456789abcdef0123456789abcdef" // exactly 32 chars

// setEnv sets the given vars for the duration of the test and clears every other
// config var, so a developer's real environment cannot influence the result.
func setEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	all := []string{
		"APP_ENV", "HTTP_PORT", "DATABASE_URL", "JWT_SECRET",
		"JWT_ACCESS_TTL", "JWT_REFRESH_TTL", "BCRYPT_COST",
		"INVOICE_EXPIRY_CHECK_INTERVAL",
		"AUTH_RATE_LIMIT_PER_MINUTE", "AUTH_RATE_LIMIT_BURST",
		"LOGIN_RATE_LIMIT_PER_MINUTE", "LOGIN_RATE_LIMIT_BURST",
		"METRICS_ENABLED",
	}
	for _, k := range all {
		t.Setenv(k, "")
	}
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

func TestLoad_Defaults(t *testing.T) {
	setEnv(t, map[string]string{"JWT_SECRET": validSecret})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppEnv != "development" {
		t.Errorf("AppEnv = %q, want development", cfg.AppEnv)
	}
	if cfg.HTTPPort != "8080" {
		t.Errorf("HTTPPort = %q, want 8080", cfg.HTTPPort)
	}
	if cfg.JWTAccessTTL != 15*time.Minute {
		t.Errorf("JWTAccessTTL = %v, want 15m", cfg.JWTAccessTTL)
	}
	if cfg.JWTRefreshTTL != 7*24*time.Hour {
		t.Errorf("JWTRefreshTTL = %v, want 168h", cfg.JWTRefreshTTL)
	}
	if cfg.BcryptCost != 12 {
		t.Errorf("BcryptCost = %d, want 12", cfg.BcryptCost)
	}
	if cfg.InvoiceExpiryCheckInterval != time.Minute {
		t.Errorf("InvoiceExpiryCheckInterval = %v, want 1m", cfg.InvoiceExpiryCheckInterval)
	}
	if !strings.HasPrefix(cfg.DatabaseURL, "postgres://") {
		t.Errorf("DatabaseURL = %q, want a postgres DSN", cfg.DatabaseURL)
	}
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":                       "production",
		"HTTP_PORT":                     "9090",
		"DATABASE_URL":                  "postgres://u:p@db:5432/x?sslmode=require",
		"JWT_SECRET":                    validSecret,
		"JWT_ACCESS_TTL":                "5m",
		"JWT_REFRESH_TTL":               "24h",
		"BCRYPT_COST":                   "6",
		"INVOICE_EXPIRY_CHECK_INTERVAL": "30s",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppEnv != "production" || cfg.HTTPPort != "9090" {
		t.Errorf("env overrides not applied: %+v", cfg)
	}
	if cfg.JWTAccessTTL != 5*time.Minute || cfg.JWTRefreshTTL != 24*time.Hour {
		t.Errorf("TTL overrides not applied: %v / %v", cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	}
	if cfg.BcryptCost != 6 {
		t.Errorf("BcryptCost = %d, want 6", cfg.BcryptCost)
	}
	if cfg.InvoiceExpiryCheckInterval != 30*time.Second {
		t.Errorf("InvoiceExpiryCheckInterval = %v, want 30s", cfg.InvoiceExpiryCheckInterval)
	}
}

// The app must refuse to boot without a usable signing key rather than silently
// running with a weak or empty one.
func TestLoad_RejectsMissingOrWeakSecret(t *testing.T) {
	cases := []struct {
		name   string
		secret string
	}{
		{"empty", ""},
		{"short", "too-short"},
		{"one char below the floor", strings.Repeat("x", minJWTSecretLen-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, map[string]string{"JWT_SECRET": tc.secret})
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted JWT_SECRET=%q", tc.secret)
			}
		})
	}
}

func TestLoad_AcceptsSecretAtExactMinimum(t *testing.T) {
	setEnv(t, map[string]string{"JWT_SECRET": strings.Repeat("x", minJWTSecretLen)})
	if _, err := Load(); err != nil {
		t.Fatalf("Load rejected a secret of exactly the minimum length: %v", err)
	}
}

func TestLoad_RejectsNonPositiveDurations(t *testing.T) {
	cases := []struct{ key, value string }{
		{"JWT_ACCESS_TTL", "0s"},
		{"JWT_ACCESS_TTL", "-5m"},
		{"JWT_REFRESH_TTL", "0s"},
		{"INVOICE_EXPIRY_CHECK_INTERVAL", "0s"},
		{"INVOICE_EXPIRY_CHECK_INTERVAL", "-1m"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			setEnv(t, map[string]string{"JWT_SECRET": validSecret, tc.key: tc.value})
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted %s=%s", tc.key, tc.value)
			}
		})
	}
}

// An unparseable value falls back to the default instead of failing the boot;
// pin that behaviour so it stays a deliberate choice.
func TestLoad_UnparseableValuesFallBackToDefaults(t *testing.T) {
	setEnv(t, map[string]string{
		"JWT_SECRET":     validSecret,
		"JWT_ACCESS_TTL": "not-a-duration",
		"BCRYPT_COST":    "not-a-number",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.JWTAccessTTL != 15*time.Minute {
		t.Errorf("JWTAccessTTL = %v, want the 15m default", cfg.JWTAccessTTL)
	}
	if cfg.BcryptCost != 12 {
		t.Errorf("BcryptCost = %d, want the default 12", cfg.BcryptCost)
	}
}

// ── rate limiting & metrics config ────────────────────────────────────────────

func TestLoad_RateLimitDefaults(t *testing.T) {
	setEnv(t, map[string]string{"JWT_SECRET": validSecret})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Defaults must actually limit something; shipping with limiting off by accident
	// would leave the brute-force gap open.
	if cfg.AuthRateLimitPerMinute <= 0 {
		t.Errorf("AuthRateLimitPerMinute = %d, want a positive default", cfg.AuthRateLimitPerMinute)
	}
	if cfg.LoginRateLimitPerMinute <= 0 {
		t.Errorf("LoginRateLimitPerMinute = %d, want a positive default", cfg.LoginRateLimitPerMinute)
	}
	// Per-account must be stricter than per-IP: one address legitimately serves many
	// users behind NAT, but one account has one owner.
	if cfg.LoginRateLimitPerMinute >= cfg.AuthRateLimitPerMinute {
		t.Errorf("per-account limit (%d) should be stricter than per-IP (%d)",
			cfg.LoginRateLimitPerMinute, cfg.AuthRateLimitPerMinute)
	}
}

func TestLoad_RateLimitOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"JWT_SECRET":                  validSecret,
		"AUTH_RATE_LIMIT_PER_MINUTE":  "100",
		"AUTH_RATE_LIMIT_BURST":       "25",
		"LOGIN_RATE_LIMIT_PER_MINUTE": "3",
		"LOGIN_RATE_LIMIT_BURST":      "3",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AuthRateLimitPerMinute != 100 || cfg.AuthRateLimitBurst != 25 {
		t.Errorf("per-IP overrides not applied: %+v", cfg)
	}
	if cfg.LoginRateLimitPerMinute != 3 || cfg.LoginRateLimitBurst != 3 {
		t.Errorf("per-account overrides not applied: %+v", cfg)
	}
}

// Zero is the documented way to switch a limiter off, so it must survive Load rather
// than being replaced by the default.
func TestLoad_RateLimitZeroDisables(t *testing.T) {
	setEnv(t, map[string]string{
		"JWT_SECRET":                  validSecret,
		"AUTH_RATE_LIMIT_PER_MINUTE":  "0",
		"LOGIN_RATE_LIMIT_PER_MINUTE": "0",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AuthRateLimitPerMinute != 0 || cfg.LoginRateLimitPerMinute != 0 {
		t.Errorf("an explicit 0 must be preserved so limiting can be disabled: %+v", cfg)
	}
}

// /metrics exposes route inventory and traffic shape, so it must be opt-in.
func TestLoad_MetricsDisabledByDefault(t *testing.T) {
	setEnv(t, map[string]string{"JWT_SECRET": validSecret})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MetricsEnabled {
		t.Error("METRICS_ENABLED must default to false")
	}
}

func TestLoad_MetricsEnabledParsing(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"t", true},
		{"false", false},
		{"0", false},
		{"not-a-bool", false}, // unparseable falls back to the default
	} {
		t.Run("METRICS_ENABLED="+tc.value, func(t *testing.T) {
			setEnv(t, map[string]string{"JWT_SECRET": validSecret, "METRICS_ENABLED": tc.value})
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.MetricsEnabled != tc.want {
				t.Errorf("MetricsEnabled = %v, want %v", cfg.MetricsEnabled, tc.want)
			}
		})
	}
}
