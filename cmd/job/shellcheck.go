package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/shellcheck"
)

// validateSubmittedScript runs the embedded bash parser plus (when available)
// the system `shellcheck` binary against the script body that `abc job run`
// is about to embed in HCL. Behavior is gated by mode:
//
//	"warn"  — parse errors block submission; shellcheck findings print as warnings.
//	"error" — shellcheck findings also block submission. Requires `shellcheck` on PATH.
//	"off"   — caller should not invoke this function.
//
// scriptBase is purely for diagnostic prefixing.
func validateSubmittedScript(ctx context.Context, stderr io.Writer, scriptBase, scriptBody, mode string) error {
	if mode != "warn" && mode != "error" {
		return fmt.Errorf("--shellcheck=%q: must be warn, error, or off", mode)
	}

	// Skip non-bash submissions outright. Detected by shebang; absence means
	// we let it through (could be raw_exec running a binary, etc.).
	if !looksLikeBash(scriptBody) {
		return nil
	}

	// Layer 1 — bash parse (always embedded).
	if perr := shellcheck.Parse(scriptBody); perr != nil {
		return fmt.Errorf("%s: bash parse error (refusing to submit broken script):\n  %s\nUse --shellcheck=off to bypass", scriptBase, perr)
	}

	// Layer 2 — system shellcheck (optional).
	out, err := shellcheck.Lint(ctx, scriptBody, shellcheck.Default())
	if errors.Is(err, shellcheck.ErrShellcheckUnavailable) {
		if mode == "error" {
			return fmt.Errorf("--shellcheck=error requires `shellcheck` on PATH (set ABC_BIN_SHELLCHECK to override)")
		}
		return nil
	}
	if err == nil {
		return nil
	}

	// shellcheck reported findings.
	if mode == "error" {
		return fmt.Errorf("%s: shellcheck reported issues (--shellcheck=error):\n%s", scriptBase, indent(out, "  "))
	}
	fmt.Fprintf(stderr, "warning: %s: shellcheck findings (use --shellcheck=error to gate, or --shellcheck=off to silence):\n%s\n", scriptBase, indent(out, "  "))
	return nil
}

func looksLikeBash(body string) bool {
	body = strings.TrimLeft(body, " \t\n")
	if !strings.HasPrefix(body, "#!") {
		// No shebang — assume bash, since `abc job run` is a shell-script entry point.
		return true
	}
	first := body
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		first = body[:i]
	}
	return strings.Contains(first, "bash") || strings.Contains(first, "/sh") || strings.Contains(first, " sh")
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
