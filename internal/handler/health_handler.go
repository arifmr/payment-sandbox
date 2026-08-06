package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Pinger is the subset of *sql.DB the readiness check needs, kept narrow so the check
// can be tested without a database.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// readinessTimeout bounds the dependency check. A probe that hangs is worse than one
// that fails: an orchestrator waiting on it learns nothing while traffic keeps arriving.
const readinessTimeout = 2 * time.Second

type HealthHandler struct {
	db  Pinger
	log *slog.Logger
}

// NewHealthHandler takes a logger directly rather than reporting through c.Error: the
// probe writes its own 503 body, and handing the error to ErrorHandler as well would
// make that middleware attempt a second write on the same response.
func NewHealthHandler(db Pinger, log *slog.Logger) *HealthHandler {
	if log == nil {
		log = slog.Default()
	}
	return &HealthHandler{db: db, log: log}
}

// Live is the liveness probe: it reports only that the process is running and never
// checks dependencies — a database outage must not make the orchestrator restart
// otherwise-healthy processes.
//
// Deliberately not annotated for Swagger: the spec's basePath is /api/v1, and the
// probes are mounted at the root, so documenting them there would advertise
// /api/v1/healthz — a path that does not exist. They are listed in the README instead.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready is the readiness probe: it reports whether this instance can actually serve
// traffic, including database reachability, and answers 503 when a dependency is down so
// the load balancer stops routing here. Not Swagger-annotated, for the reason on Live.
func (h *HealthHandler) Ready(c *gin.Context) {
	// Liveness and readiness answer different questions, and conflating them causes
	// real outages. Liveness = "is this process broken, restart it". Readiness = "can
	// this process serve right now, route to it". If liveness also checked the
	// database, a brief database blip would make every replica fail its liveness probe
	// at once and the orchestrator would restart the entire fleet — turning a
	// recoverable dependency problem into a full outage.
	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		// The error itself is never returned to the caller: it can carry the DSN, host
		// names and credentials. It is logged, and the probe reports only the verdict.
		h.log.Error("readiness_check_failed",
			slog.String("dependency", "database"),
			slog.String("error", err.Error()),
		)
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"reason": "database",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
