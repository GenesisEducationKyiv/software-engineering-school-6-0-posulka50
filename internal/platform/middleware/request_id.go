package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/posul/github-notifier/internal/platform/logctx"
)

const requestIDHeader = "X-Request-Id"

// RequestID assigns each incoming request a correlation id, stores it on
// the request context (so downstream slog calls via logctx.Handler include
// it), and echoes it back on the X-Request-Id response header. If the
// client already sent an X-Request-Id header it is reused so cross-service
// traces stay stitched together.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		ctx := logctx.WithRequestID(c.Request.Context(), id)
		c.Request = c.Request.WithContext(ctx)
		c.Writer.Header().Set(requestIDHeader, id)
		c.Next()
	}
}
