package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dboarif/payment-sandbox/internal/pkg/metrics"
)

// Metrics records request latency so the SRS §5.1 target (≤ 300 ms) is measurable.
func Metrics(reg *metrics.Registry) gin.HandlerFunc {
	if reg == nil {
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		// c.FullPath() is the matched route pattern ("/api/v1/invoices/:id"), not the
		// concrete path. Labelling by the concrete path would mint a new time series
		// per invoice id — unbounded cardinality is the classic way to take down a
		// metrics backend with your own instrumentation. Unmatched routes return "",
		// which the registry folds into a single "unmatched" series.
		reg.Observe(c.Request.Method, c.FullPath(), c.Writer.Status(), time.Since(start).Seconds())
	}
}
