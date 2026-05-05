package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dboarif/payment-sandbox/internal/model"
	"github.com/dboarif/payment-sandbox/internal/pkg/jwt"
)

const (
	ctxUserID = "auth.user_id"
	ctxRole   = "auth.role"
)

// Auth requires a valid bearer token. Optional=true allows missing tokens but still parses present ones.
func Auth(j *jwt.Manager, optional bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			if optional {
				c.Next()
				return
			}
			abortUnauthorized(c, "missing Authorization header")
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			abortUnauthorized(c, "invalid Authorization header")
			return
		}
		claims, err := j.Parse(parts[1])
		if err != nil {
			abortUnauthorized(c, "invalid token")
			return
		}
		c.Set(ctxUserID, claims.UserID)
		c.Set(ctxRole, claims.Role)
		c.Next()
	}
}

func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get(ctxRole)
		roleStr, _ := role.(string)
		for _, r := range roles {
			if roleStr == r {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, model.ErrorResponse{
			Error: model.ErrorBody{Code: "FORBIDDEN", Message: "insufficient permissions"},
		})
	}
}

func UserID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxUserID)
	if !ok {
		return uuid.Nil, false
	}
	s, ok := v.(string)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func Role(c *gin.Context) string {
	v, _ := c.Get(ctxRole)
	s, _ := v.(string)
	return s
}

func abortUnauthorized(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, model.ErrorResponse{
		Error: model.ErrorBody{Code: "UNAUTHORIZED", Message: msg},
	})
}
