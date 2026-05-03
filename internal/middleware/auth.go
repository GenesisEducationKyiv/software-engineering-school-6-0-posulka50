package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// APIKeyAuth returns a Gin middleware that enforces X-API-Key header authentication.
// If apiKey is empty, the middleware is a no-op and all requests are allowed.
func APIKeyAuth(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey == "" {
			c.Next()
			return
		}
		if c.GetHeader("X-API-Key") != apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or invalid API key",
			})
			return
		}
		c.Next()
	}
}
