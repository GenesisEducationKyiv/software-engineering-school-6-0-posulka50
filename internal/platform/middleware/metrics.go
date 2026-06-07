package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/posul/github-notifier/internal/platform/metrics"
)

func Prometheus() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		metrics.HTTPRequestsTotal.WithLabelValues(c.Request.Method, c.FullPath(), status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(c.Request.Method, c.FullPath()).Observe(duration)
	}
}
