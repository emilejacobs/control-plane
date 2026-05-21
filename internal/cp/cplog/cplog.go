package cplog

import (
	"io"
	"log/slog"
)

// New builds a slog.Logger emitting JSON to w with the ADR-011 standard
// shape: ts, level, service, msg on every line. Pre-binds the service
// field; correlation_id is added per-request by the Middleware.
//
// Pass os.Stdout in production. Pass a *bytes.Buffer in tests.
func New(w io.Writer, service string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Key = "ts"
			}
			return a
		},
	})
	return slog.New(h).With("service", service)
}
