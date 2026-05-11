package envvars

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Bootstrap-time helpers that read env directly (no flag, no context) and
// walk the canonical-name → aliases chain. Use these in init code, config
// loaders, and any place that doesn't have a Resolver in scope.
//
// For command bodies that DO have cobra flags and a loaded context,
// prefer Resolver.Resolve — it gives full precedence and source tracking.

// bootstrapWarnings tracks one-time deprecation warnings emitted by
// LookupEnv-family helpers. Resolver instances have their own per-call
// state; this is the process-wide fallback for code paths that never
// construct a Resolver.
var (
	bootstrapWarnMu sync.Mutex
	bootstrapWarned = map[string]struct{}{}
)

func emitBootstrapWarn(key, msg string) {
	bootstrapWarnMu.Lock()
	if _, seen := bootstrapWarned[key]; seen {
		bootstrapWarnMu.Unlock()
		return
	}
	bootstrapWarned[key] = struct{}{}
	bootstrapWarnMu.Unlock()
	fmt.Fprintln(os.Stderr, msg)
}

// LookupEnv reads canonicalName from the OS environment, falling back to
// any of its registered aliases. Returns the resolved value, the Source
// that won (SourceABCEnv, SourceABCEnvAlias, or SourceUnset), and ok.
//
// When an alias is hit, a one-time deprecation warning is emitted on
// stderr (per process — see bootstrapWarned).
//
// Use this in bootstrap paths (config.Load, debuglog init, etc.) where
// no Resolver is constructed and there's no flag/context to consult.
// Use Resolver.Resolve for full precedence inside command handlers.
//
// Panics if canonicalName is not in the registry — that's a programmer
// error, not a runtime condition.
func LookupEnv(canonicalName string) (string, Source, bool) {
	entry, found := Lookup(canonicalName)
	if !found {
		panic(fmt.Sprintf("envvars.LookupEnv: unknown name %q (not in registry)", canonicalName))
	}
	if entry.Name != canonicalName {
		// Caller passed an alias as if it were canonical. Allow but treat
		// as canonical-of-record so the alias-warning logic still works
		// when other aliases of the same entry are set.
		canonicalName = entry.Name
	}

	// 1. Canonical
	if v, ok := os.LookupEnv(canonicalName); ok {
		return v, SourceABCEnv, true
	}
	// 2. Aliases
	for _, alias := range entry.Aliases {
		if v, ok := os.LookupEnv(alias); ok {
			emitBootstrapWarn("alias:"+alias,
				fmt.Sprintf("warning: env var %s is deprecated; use %s instead",
					alias, canonicalName))
			return v, SourceABCEnvAlias, true
		}
	}
	return "", SourceUnset, false
}

// Get is a convenience that returns just the value (empty string when
// unset). Equivalent to os.Getenv but walks aliases.
func Get(canonicalName string) string {
	v, _, _ := LookupEnv(canonicalName)
	return v
}

// IsSet reports whether the canonical name or any of its aliases is set
// in the environment (LookupEnv-style: explicit empty counts as set).
func IsSet(canonicalName string) bool {
	_, _, ok := LookupEnv(canonicalName)
	return ok
}

// IsTruthy returns true when LookupEnv yields one of the standard
// truthy strings (1 / true / yes / y / on, case-insensitive). Useful
// for boolean-flag env vars like ABC_CLI_DEBUG.
func IsTruthy(canonicalName string) bool {
	v, _, ok := LookupEnv(canonicalName)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

// resetBootstrapWarnings is exported for tests only via the test file.
// Production code never calls this — process-lifetime de-dup is the
// design intent.
func resetBootstrapWarnings() {
	bootstrapWarnMu.Lock()
	bootstrapWarned = map[string]struct{}{}
	bootstrapWarnMu.Unlock()
}
