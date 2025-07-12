package log

import "context"

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

// Context keys for storing log metadata.
const (
	// TraceIDKey is the context key for trace IDs.
	TraceIDKey ContextKey = "trace_id"
	// LogFieldsKey is the context key for additional log fields.
	LogFieldsKey ContextKey = "log_fields"
)

// LogFields represents a collection of structured log fields.
type LogFields map[string]any

// WithFields adds or updates log fields in the context.
// If fields already exist in the context, they will be merged with new fields overwriting existing ones.
func WithFields(ctx context.Context, fields LogFields) context.Context {
	existing := GetLogFields(ctx)
	merged := make(LogFields)

	// Copy existing fields
	for k, v := range existing {
		merged[k] = v
	}

	// Add/overwrite with new fields
	for k, v := range fields {
		merged[k] = v
	}

	return context.WithValue(ctx, LogFieldsKey, merged)
}

// GetLogFields retrieves log fields from the context.
// Returns an empty LogFields if none are found.
func GetLogFields(ctx context.Context) LogFields {
	if fields, ok := ctx.Value(LogFieldsKey).(LogFields); ok {
		return fields
	}
	return make(LogFields)
}
