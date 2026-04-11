// Package debuglog provides structured JSON debug logging for the abc CLI.
//
// Usage:
//
//	// In root PersistentPreRunE:
//	ctx, cfg, err := debuglog.Init(cmd.Context(), level)
//	defer cfg.Close() // flushes and prints final path to stderr
//	cmd.SetContext(ctx)
//
//	// Anywhere in a command:
//	log := debuglog.FromContext(ctx)
//	log.Info("ssh.dial.start", debuglog.AttrsSSHDial(host, port, user, methods, jump)...)
//
// When level == 0 (default, --debug not set) Init returns a context with a
// noop logger — zero allocations, zero overhead.
package debuglog

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Level constants matching the plan verbosity table.
//
//	L1 (--debug)    → slog.LevelInfo   — AI-debuggable events
//	L2 (--debug=2)  → slog.LevelDebug  — includes remote commands, raw output
//	L3 (--debug=3)  → LevelTrace       — SSH round-trips, full HCL, timing detail
const (
	L1 = slog.LevelInfo
	L2 = slog.LevelDebug
	L3 = LevelTrace // defined in events.go as slog.Level(-4)
)

// Config holds the state created by Init. Always non-nil after Init returns.
type Config struct {
	Enabled  bool
	Level    int
	FilePath string
	file     *os.File
	logger   *slog.Logger
}

// Close flushes any buffered output and closes the underlying log file.
// It is a no-op when Enabled is false. Safe to call multiple times.
func (c *Config) Close() {
	if c == nil || !c.Enabled || c.file == nil {
		return
	}
	_ = c.file.Sync()
	_ = c.file.Close()
	c.file = nil
}

// ─── context key ─────────────────────────────────────────────────────────────

type ctxKey struct{}

// WithLogger stores l in ctx and returns the enriched context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the debug logger stored in ctx, or a noop logger if
// none is present. Callers never need to nil-check the returned logger.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return noopLogger
}

// ─── Init ─────────────────────────────────────────────────────────────────────

// Init initialises debug logging for a CLI run.
//
// level is the verbosity (0 = off, 1–3 = increasing detail). When level == 0
// Init returns immediately with a noop-logger context and no files are opened.
//
// The caller should call cfg.Close() (typically via defer) after the command
// completes to flush and close the log file.
func Init(ctx context.Context, level int) (context.Context, *Config, error) {
	cfg := &Config{Level: level}

	if level <= 0 {
		return ctx, cfg, nil
	}
	cfg.Enabled = true

	// Resolve the slog minimum level from the verbosity integer.
	var minLevel slog.Level
	switch level {
	case 1:
		minLevel = L1 // INFO and above
	case 2:
		minLevel = L2 // DEBUG and above
	default: // 3+
		minLevel = L3 // TRACE and above
	}

	// Build log file path.
	dir := logDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ctx, cfg, fmt.Errorf("create debug log dir %s: %w", dir, err)
	}
	ts := time.Now().UTC().Format("2006-01-02T15-04-05")
	cfg.FilePath = filepath.Join(dir, "debug-"+ts+".log")

	f, err := os.OpenFile(cfg.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return ctx, cfg, fmt.Errorf("open debug log %s: %w", cfg.FilePath, err)
	}
	cfg.file = f

	// Build the handler chain: JSON → Redacting wrapper.
	jsonH := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level:     minLevel,
		AddSource: false,
	})
	cfg.logger = slog.New(NewRedactingHandler(jsonH))

	ctx = WithLogger(ctx, cfg.logger)
	return ctx, cfg, nil
}

// PrintHeader prints the "[abc debug]" line to stderr at the start of a run.
func (c *Config) PrintHeader(stderr *os.File) {
	if c == nil || !c.Enabled {
		return
	}
	fmt.Fprintf(stderr, "[abc debug] level=%d log: %s\n", c.Level, c.FilePath)
}

// PrintFooter prints the closing path line (and, on error, the support hint).
func (c *Config) PrintFooter(stderr *os.File, runErr error) {
	if c == nil || !c.Enabled {
		return
	}
	fmt.Fprintf(stderr, "[abc debug] log: %s\n", c.FilePath)
	if runErr != nil {
		fmt.Fprintf(stderr, "[abc debug] operation failed — attach the log above when reporting issues\n")
	}
}
