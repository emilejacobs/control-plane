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
