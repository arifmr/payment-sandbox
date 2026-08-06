package middleware

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

// discardLogger silences output for tests that do not inspect logs.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// captureLogger returns a logger writing into buf so a test can assert on what
// was recorded server-side.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

// serveWithErrorHandler runs one request through RequestID → ErrorHandler → handler.
func serveWithErrorHandler(log *slog.Logger, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(RequestID())
	r.Use(ErrorHandler(log))
	r.GET("/x", handler)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec
}

func TestErrorHandler_MapsDomainErrorToStatusAndBody(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"not found", apperror.ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"forbidden", apperror.ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		{"invalid state", apperror.ErrInvalidState, http.StatusUnprocessableEntity, "INVALID_STATE"},
		{"conflict", apperror.ErrEmailTaken, http.StatusConflict, "EMAIL_TAKEN"},
		{"bad request", apperror.ErrInvalidAmount, http.StatusBadRequest, "INVALID_AMOUNT"},
		{"unauthorized", apperror.ErrInvalidCredentials, http.StatusUnauthorized, "INVALID_CREDENTIALS"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveWithErrorHandler(discardLogger(), func(c *gin.Context) {
				c.Error(tc.err)
			})

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body %q should contain code %q", rec.Body.String(), tc.wantBody)
			}
			// Contract from the README: { "error": { "code", "message" } }
			if !strings.Contains(rec.Body.String(), `"error"`) {
				t.Errorf("body %q is missing the error envelope", rec.Body.String())
			}
		})
	}
}

// SRS §5.2: internal details must never reach the client.
func TestErrorHandler_HidesInternalDetailButLogsThem(t *testing.T) {
	var logs bytes.Buffer
	const secret = "pq: password authentication failed for user postgres"

	rec := serveWithErrorHandler(captureLogger(&logs), func(c *gin.Context) {
		c.Error(errors.New(secret))
	})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Errorf("response leaked internal detail: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "INTERNAL") {
		t.Errorf("body %q should carry the generic INTERNAL code", rec.Body.String())
	}
	if !strings.Contains(logs.String(), secret) {
		t.Error("the underlying cause must still be logged server-side")
	}
	if !strings.Contains(logs.String(), "request_id") {
		t.Error("internal errors should be logged with the request id for correlation")
	}
}

// A wrapped domain error must keep its status rather than degrading to 500.
func TestErrorHandler_UnwrapsDomainErrorFromWrapper(t *testing.T) {
	rec := serveWithErrorHandler(discardLogger(), func(c *gin.Context) {
		c.Error(apperror.Wrap(apperror.KindNotFound, "NOT_FOUND", "resource not found", errors.New("sql: no rows")))
	})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "sql: no rows") {
		t.Errorf("driver detail leaked into the response: %s", rec.Body.String())
	}
}

func TestErrorHandler_PassesThroughSuccessfulResponses(t *testing.T) {
	rec := serveWithErrorHandler(discardLogger(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

// When several errors accumulate, the most recent one decides the response.
func TestErrorHandler_UsesLastError(t *testing.T) {
	rec := serveWithErrorHandler(discardLogger(), func(c *gin.Context) {
		c.Error(apperror.ErrNotFound)
		c.Error(apperror.ErrForbidden)
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (the last error)", rec.Code)
	}
}

func TestRecovery_TurnsPanicIntoGeneric500(t *testing.T) {
	var logs bytes.Buffer

	r := gin.New()
	r.Use(RequestID())
	r.Use(Recovery(captureLogger(&logs)))
	r.Use(ErrorHandler(discardLogger()))
	r.GET("/boom", func(c *gin.Context) { panic("secret internal panic detail") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret internal panic detail") {
		t.Errorf("panic detail leaked to the client: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "panic") {
		t.Error("the panic must be logged")
	}
}

// ── RequestID / Logger ────────────────────────────────────────────────────────

func TestRequestID_GeneratesAndEchoesHeader(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	var inContext string
	r.GET("/x", func(c *gin.Context) {
		v, _ := c.Get(ctxRequestID)
		inContext = asString(v)
		c.Status(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	header := rec.Header().Get("X-Request-ID")
	if header == "" {
		t.Fatal("X-Request-ID response header is missing")
	}
	if inContext != header {
		t.Errorf("context id %q does not match header %q", inContext, header)
	}
}

// An upstream-supplied id must be preserved so traces stitch together.
func TestRequestID_HonoursIncomingHeader(t *testing.T) {
	const incoming = "trace-from-upstream-123"

	r := gin.New()
	r.Use(RequestID())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", incoming)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != incoming {
		t.Errorf("X-Request-ID = %q, want the incoming %q", got, incoming)
	}
}

// SRS §3.5: structured logging with the fields needed to trace a request.
func TestLogger_EmitsStructuredRequestLine(t *testing.T) {
	var logs bytes.Buffer

	r := gin.New()
	r.Use(RequestID())
	r.Use(Logger(captureLogger(&logs)))
	r.GET("/invoices", func(c *gin.Context) { c.Status(http.StatusCreated) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/invoices", nil))

	out := logs.String()
	for _, want := range []string{"http_request", "method=GET", "path=/invoices", "status=201", "request_id=", "duration="} {
		if !strings.Contains(out, want) {
			t.Errorf("log line %q is missing %q", out, want)
		}
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
}

func TestAsString(t *testing.T) {
	if got := asString(nil); got != "" {
		t.Errorf("asString(nil) = %q, want empty", got)
	}
	if got := asString("abc"); got != "abc" {
		t.Errorf("asString(\"abc\") = %q", got)
	}
	if got := asString(123); got != "" {
		t.Errorf("asString(123) = %q, want empty", got)
	}
}
