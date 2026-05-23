package cplog

import (
	"context"
	"log/slog"
)

type ctxKey struct{}

func withLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the request-scoped logger if the cplog middleware
// installed one, otherwise slog.Default(). Handler code should always go
// through FromContext so log lines carry the inbound correlation_id.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

type correlationIDKey struct{}

// WithCorrelationID returns ctx carrying the given id. The Middleware sets
// this alongside the request-scoped logger so non-logger code paths (audit
// writes, downstream RPCs) can pull the id without parsing slog attrs.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey{}, id)
}

// CorrelationIDFromContext returns the request's correlation_id when the
// Middleware installed one, otherwise the empty string.
func CorrelationIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey{}).(string); ok {
		return id
	}
	return ""
}
