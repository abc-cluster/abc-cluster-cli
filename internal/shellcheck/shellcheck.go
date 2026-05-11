// Package shellcheck validates bash scripts that abc generates (Nomad heredocs,
// prestart hooks, runtime wrappers).
//
// Two layers of validation:
//
//  1. [Parse] — pure-Go bash parser via mvdan.cc/sh/v3/syntax. Catches syntax
//     errors (unbalanced quotes, missing fi/done, malformed heredocs). Embedded
//     in the abc binary; runs at codegen time on every user's machine.
//
//  2. [Lint] — runs the system `shellcheck` binary (if present on PATH) for the
//     full SC* rule set. No-op when shellcheck is unavailable, since the bash
//     parser already caught the structural issues. Use ABC_SHELLCHECK_BIN to
//     override the binary location.
//
// Plus abc-specific pre-checks for Nomad-heredoc gotchas — see [LintNomadHeredoc].
package shellcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
)

// ErrShellcheckUnavailable is returned by [Lint] when no `shellcheck` binary
// is on PATH and ABC_SHELLCHECK_BIN is unset. Callers that want a hard gate
// in CI should treat this as a failure; callers in user-facing flows should
// treat it as a soft skip.
var ErrShellcheckUnavailable = errors.New("shellcheck binary not found on PATH (set ABC_SHELLCHECK_BIN to override)")

// Options controls a single shellcheck invocation.
type Options struct {
	Shell    string   // bash, sh, dash, ksh (empty = shellcheck default)
	Severity string   // error, warning, info, style
	Format   string   // gcc, tty, json, json1, checkstyle (default: gcc)
	Excludes []string // SC codes to suppress, e.g. SC2034
}

// Default returns sensible options: bash dialect, warning+, gcc-format output.
func Default() Options {
	return Options{Shell: "bash", Severity: "warning", Format: "gcc"}
}

// Parse runs the embedded Go bash parser. Returns nil on a syntactically valid
// script. Always available — no system dependency.
func Parse(script string) error {
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	_, err := p.Parse(strings.NewReader(script), "")
	return err
}

// Lint pipes script through the system `shellcheck` binary via stdin. On a
// clean run returns ("", nil). On findings returns (output, non-nil error).
// If shellcheck is not on PATH, returns ("", ErrShellcheckUnavailable).
func Lint(ctx context.Context, script string, opts Options) (string, error) {
	bin, err := resolveShellcheck()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, bin, scArgs(opts)...)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return "", nil
	}
	out := strings.TrimRight(stdout.String()+stderr.String(), "\n")
	return out, fmt.Errorf("shellcheck reported issues: %w", runErr)
}

// HasShellcheck reports whether a `shellcheck` binary is available.
func HasShellcheck() bool {
	_, err := resolveShellcheck()
	return err == nil
}

func resolveShellcheck() (string, error) {
	if env := envvars.Get("ABC_SHELLCHECK_BIN"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
		return "", fmt.Errorf("ABC_SHELLCHECK_BIN=%s: %w", env, ErrShellcheckUnavailable)
	}
	if bin, err := exec.LookPath("shellcheck"); err == nil {
		return bin, nil
	}
	return "", ErrShellcheckUnavailable
}

func scArgs(opts Options) []string {
	var a []string
	if opts.Shell != "" {
		a = append(a, "--shell="+opts.Shell)
	}
	if opts.Severity != "" {
		a = append(a, "--severity="+opts.Severity)
	}
	if opts.Format != "" {
		a = append(a, "--format="+opts.Format)
	} else {
		a = append(a, "--format=gcc")
	}
	for _, e := range opts.Excludes {
		a = append(a, "--exclude="+e)
	}
	return append(a, "-")
}
