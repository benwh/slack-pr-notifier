package middleware

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LoggingMiddleware adds correlation IDs and structured logging to requests.
func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Generate correlation ID
		correlationID := c.GetHeader("X-Correlation-ID")
		if correlationID == "" {
			correlationID = uuid.New().String()
		}

		// Add correlation ID to context
		c.Set("correlation_id", correlationID)
		c.Header("X-Correlation-ID", correlationID)

		// Log request
		slog.Info("Request started",
			"correlation_id", correlationID,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"user_agent", c.Request.UserAgent(),
			"remote_addr", c.ClientIP(),
		)

		// Process request
		c.Next()

		// Log response
		slog.Info("Request completed",
			"correlation_id", correlationID,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
		)
	}
}
