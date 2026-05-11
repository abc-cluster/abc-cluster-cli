package utils

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// shadowingWarnings tracks the keys we have already warned for in this
// CLI invocation, so a single command that fans out to multiple
// resolvers doesn't double-warn.
var (
	shadowingWarnMu sync.Mutex
	shadowingWarned = map[string]struct{}{}
)

// UpsertEnvHonouringSelector merges resolved cred_source values into the
// base environ with selector-aware precedence:
//
//   - selection == "local" (the default): behaves like
//     UpsertEnvOnlyMissing — the parent shell value wins when present.
//   - selection == "nomad" / "vault": the resolved value REPLACES the
//     parent shell value, and a one-time stderr warning is emitted per
//     env-var key citing the explicit selector.
//
// This honours the spec contract that `--config nomad/vault` is an
// explicit operator instruction to use that backend; silently letting
// stray shell env vars shadow the resolution would defeat the selector.
//
// stderr may be nil (warnings are then silenced).
//
// See brainstorms/cli-env-resolution/2026-05-11-cred-source-shadowing-audit.md.
func UpsertEnvHonouringSelector(base []string, extra map[string]string, selection string, stderr io.Writer) []string {
	if len(extra) == 0 {
		return base
	}
	sel := strings.TrimSpace(strings.ToLower(selection))
	if sel == "" || sel == CredConfigLocal {
		return UpsertEnvOnlyMissing(base, extra)
	}

	// Non-local selector: force-overwrite, but emit one-time warnings
	// when the parent shell value would have been replaced.
	for k, v := range extra {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		if parent := strings.TrimSpace(GetenvFromEnviron(base, k)); parent != "" && parent != strings.TrimSpace(v) {
			warnShadowedKey(stderr, k, sel)
		}
	}
	return upsertEnv(base, extra)
}

func warnShadowedKey(stderr io.Writer, key, selection string) {
	if stderr == nil {
		return
	}
	shadowingWarnMu.Lock()
	if _, seen := shadowingWarned[key]; seen {
		shadowingWarnMu.Unlock()
		return
	}
	shadowingWarned[key] = struct{}{}
	shadowingWarnMu.Unlock()
	fmt.Fprintf(stderr,
		"warning: %s is set in shell but ignored; --config %s is authoritative for this invocation\n",
		key, selection)
}

// ResetShadowingWarningsForTest is exposed for unit tests that need a
// fresh state across cases. Production code never calls this.
func ResetShadowingWarningsForTest() {
	shadowingWarnMu.Lock()
	shadowingWarned = map[string]struct{}{}
	shadowingWarnMu.Unlock()
}
