package debuglog

import (
	"context"
	"log/slog"
)

// noopHandler is a slog.Handler that discards all records.
//
// Enabled() always returns false, so slog short-circuits before ever calling
// Handle() — zero allocations, zero overhead when --debug is not set.
type noopHandler struct{}

func (noopHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (noopHandler) Handle(context.Context, slog.Record) error { return nil }
func (noopHandler) WithAttrs([]slog.Attr) slog.Handler        { return noopHandler{} }
func (noopHandler) WithGroup(string) slog.Handler             { return noopHandler{} }

// noopLogger is a pre-built *slog.Logger backed by noopHandler, returned
// by FromContext when no debug logger is present in the context.
var noopLogger = slog.New(noopHandler{})
