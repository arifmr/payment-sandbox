package middleware

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/apperror"
)

// ErrorHandler maps errors stored on the gin.Context (via c.Error) to JSON responses.
// Handlers should call c.Error(err); return — and never write themselves on the error path.
func ErrorHandler(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err
		var ae *apperror.Error
		if errors.As(err, &ae) {
			c.AbortWithStatusJSON(apperror.HTTPStatus(err), model.ErrorResponse{
				Error: model.ErrorBody{Code: ae.Code, Message: ae.Message},
			})
			return
		}

		// Unknown / internal error — log details, return generic message.
		reqID, _ := c.Get(ctxRequestID)
		log.Error("internal_error",
			slog.String("request_id", asString(reqID)),
			slog.String("path", c.Request.URL.Path),
			slog.String("error", err.Error()),
		)
		c.AbortWithStatusJSON(http.StatusInternalServerError, model.ErrorResponse{
			Error: model.ErrorBody{Code: "INTERNAL", Message: "internal server error"},
		})
	}
}

// Recovery converts panics to errors and lets ErrorHandler emit the response.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		reqID, _ := c.Get(ctxRequestID)
		log.Error("panic",
			slog.String("request_id", asString(reqID)),
			slog.Any("panic", recovered),
		)
		c.AbortWithStatusJSON(http.StatusInternalServerError, model.ErrorResponse{
			Error: model.ErrorBody{Code: "INTERNAL", Message: "internal server error"},
		})
	})
}
