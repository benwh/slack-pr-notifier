package log

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

// TraceIDKey is the context key for trace IDs.
const TraceIDKey ContextKey = "trace_id"
