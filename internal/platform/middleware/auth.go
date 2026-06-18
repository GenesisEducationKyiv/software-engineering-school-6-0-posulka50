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

// InternalAuth returns a Gin middleware enforcing an internal shared-secret
// token via the X-Internal-Token header. If token is empty the middleware is
// a no-op so local dev without the env var still works.
func InternalAuth(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		if c.GetHeader("X-Internal-Token") != token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or invalid internal token",
			})
			return
		}
		c.Next()
	}
}
