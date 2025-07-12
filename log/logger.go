package log

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"
)

// Logger is the package-level logger instance.
var Logger *slog.Logger

//nolint:gochecknoinits
func init() {
	Logger = slog.Default()
}

// WithTrace returns a logger that includes trace_id if available from context or gin context.
func WithTrace(ctx interface{}) *slog.Logger {
	var traceID string

	// Try to extract trace_id from different context types
	switch v := ctx.(type) {
	case *gin.Context:
		traceID = v.GetString("trace_id")
	case context.Context:
		if id, ok := v.Value(TraceIDKey).(string); ok {
			traceID = id
		}
	}

	if traceID != "" {
		return Logger.With("trace_id", traceID)
	}
	return Logger
}

// Context-aware logging methods that automatically extract trace_id from context

// Info logs at Info level with automatic trace_id extraction from context.
func Info(ctx context.Context, msg string, args ...any) {
	WithTrace(ctx).Info(msg, args...)
}

// Error logs at Error level with automatic trace_id extraction from context.
func Error(ctx context.Context, msg string, args ...any) {
	WithTrace(ctx).Error(msg, args...)
}

// Warn logs at Warn level with automatic trace_id extraction from context.
func Warn(ctx context.Context, msg string, args ...any) {
	WithTrace(ctx).Warn(msg, args...)
}

// Debug logs at Debug level with automatic trace_id extraction from context.
func Debug(ctx context.Context, msg string, args ...any) {
	WithTrace(ctx).Debug(msg, args...)
}
