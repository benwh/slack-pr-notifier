package middleware

import (
	"context"
	"strings"
	"time"

	"github-slack-notifier/internal/log"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LoggingMiddleware adds trace IDs and structured logging to requests.
func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Generate trace ID - check Cloud Run trace header first
		traceID := c.GetHeader("X-Cloud-Trace-Context")
		if traceID != "" {
			// Extract trace ID from Cloud Run format: "TRACE_ID/SPAN_ID;o=TRACE_TRUE"
			if slashIndex := strings.Index(traceID, "/"); slashIndex != -1 {
				traceID = traceID[:slashIndex]
			}
		} else {
			// Fallback to X-Trace-ID header or generate new one
			traceID = c.GetHeader("X-Trace-ID")
			if traceID == "" {
				traceID = uuid.New().String()
			}
		}

		// Add trace ID to gin context and request context
		c.Set("trace_id", traceID)
		c.Header("X-Trace-ID", traceID)

		// Store trace_id in request context for downstream handlers
		ctx := context.WithValue(c.Request.Context(), log.TraceIDKey, traceID)
		c.Request = c.Request.WithContext(ctx)

		// Log request
		startTime := time.Now()
		logger := log.WithTrace(c)
		logger.Debug("Request started",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"user_agent", c.Request.UserAgent(),
			"remote_addr", c.ClientIP(),
		)

		// Process request
		c.Next()

		// Log response
		duration := time.Since(startTime)
		logger.Info("Request completed",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_seconds", duration.Seconds(),
		)
	}
}
