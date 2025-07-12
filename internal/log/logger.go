package log

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"
)

// Logger returns the current default logger instance.
func Logger() *slog.Logger {
	return slog.Default()
}

// WithContext returns a logger that includes trace_id and any additional log fields from context.
func WithContext(ctx interface{}) *slog.Logger {
	logger := Logger()
	var traceID string
	var logFields LogFields

	// Try to extract trace_id and log fields from different context types
	switch v := ctx.(type) {
	case *gin.Context:
		traceID = v.GetString("trace_id")
		// For gin.Context, check if there's a standard context with log fields
		if v.Request != nil && v.Request.Context() != nil {
			logFields = GetLogFields(v.Request.Context())
		}
	case context.Context:
		if id, ok := v.Value(TraceIDKey).(string); ok {
			traceID = id
		}
		logFields = GetLogFields(v)
	}

	// Add trace_id if present
	if traceID != "" {
		logger = logger.With("trace_id", traceID)
	}

	// Add all log fields from context
	for k, v := range logFields {
		logger = logger.With(k, v)
	}

	return logger
}

// WithTrace is an alias for WithContext for backward compatibility.
func WithTrace(ctx interface{}) *slog.Logger {
	return WithContext(ctx)
}

// Context-aware logging methods that automatically extract trace_id and log fields from context

// Info logs at Info level with automatic trace_id and field extraction from context.
func Info(ctx context.Context, msg string, args ...any) {
	WithContext(ctx).Info(msg, args...) //nolint:contextcheck // WithContext extracts metadata from context
}

// Error logs at Error level with automatic trace_id and field extraction from context.
func Error(ctx context.Context, msg string, args ...any) {
	WithContext(ctx).Error(msg, args...) //nolint:contextcheck // WithContext extracts metadata from context
}

// Warn logs at Warn level with automatic trace_id and field extraction from context.
func Warn(ctx context.Context, msg string, args ...any) {
	WithContext(ctx).Warn(msg, args...) //nolint:contextcheck // WithContext extracts metadata from context
}

// Debug logs at Debug level with automatic trace_id and field extraction from context.
func Debug(ctx context.Context, msg string, args ...any) {
	WithContext(ctx).Debug(msg, args...) //nolint:contextcheck // WithContext extracts metadata from context
}
