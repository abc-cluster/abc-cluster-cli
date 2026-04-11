package debuglog

import (
	"context"
	"log/slog"
)

// RedactingHandler wraps an inner slog.Handler and scrubs every slog.Record
// before passing it downstream. It applies:
//   - Sensitive field-name redaction (passwords, tokens, keys)
//   - Value-pattern redaction (PEM blocks, Tailscale auth keys, Bearer tokens, …)
//
// This is a security boundary: no sensitive data should reach the log file
// even if a caller accidentally passes it as a raw slog.Attr.
type RedactingHandler struct {
	inner slog.Handler
}

// NewRedactingHandler wraps inner with redaction logic.
func NewRedactingHandler(inner slog.Handler) *RedactingHandler {
	return &RedactingHandler{inner: inner}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Build a cleaned copy: same time/level/msg, scrubbed attrs.
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(scrubAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cleaned := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		cleaned[i] = scrubAttr(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(cleaned)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name)}
}
