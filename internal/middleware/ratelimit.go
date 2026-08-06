package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/ratelimit"
)

// RateLimitByIP rejects requests once a client address exceeds the limiter's budget.
//
// Applied to the auth endpoints, where an unauthenticated caller can otherwise attempt
// credentials as fast as bcrypt allows. Keyed by c.ClientIP(), which honours
// X-Forwarded-For only for proxies Gin is configured to trust — otherwise a caller
// could forge the header and get a fresh bucket per request.
func RateLimitByIP(l *ratelimit.Limiter) gin.HandlerFunc {
	if l == nil || !l.Enabled() {
		// Nothing to enforce: hand back a pass-through so the router does not need to
		// branch on whether limiting is configured.
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		key := c.ClientIP()
		if l.Allow(key) {
			c.Next()
			return
		}
		abortRateLimited(c, l, key)
	}
}

// abortRateLimited answers 429 with Retry-After so a well-behaved client can back off
// instead of hammering.
func abortRateLimited(c *gin.Context, l *ratelimit.Limiter, key string) {
	if wait := l.RetryAfter(key); wait > 0 {
		seconds := int(wait.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, model.ErrorResponse{
		Error: model.ErrorBody{
			Code:    "RATE_LIMITED",
			Message: "too many requests, please retry later",
		},
	})
}

// RateLimited writes the 429 response for callers that discover the limit after body
// binding — the per-account login limit needs the email, which is only known then.
func RateLimited(c *gin.Context, l *ratelimit.Limiter, key string) {
	abortRateLimited(c, l, key)
}
