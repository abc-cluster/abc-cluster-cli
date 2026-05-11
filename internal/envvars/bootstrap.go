package envvars

import (
	"fmt"
	"os"
	"strings"
)

// Bootstrap-time helpers that read env directly (no flag, no context).
// Use these in init code, config loaders, and any place that doesn't have
// a Resolver in scope.
//
// For command bodies that DO have cobra flags and a loaded context,
// prefer Resolver.Resolve — it gives full precedence and source tracking.

// LookupEnv reads the canonical env var from the OS environment.
// Returns the value, the Source that won (SourceABCEnv or SourceUnset),
// and ok.
//
// Use this in bootstrap paths (config.Load, debuglog init, etc.) where
// no Resolver is constructed and there's no flag/context to consult.
// Use Resolver.Resolve for full precedence inside command handlers.
//
// Panics if canonicalName is not in the registry — that's a programmer
// error, not a runtime condition.
func LookupEnv(canonicalName string) (string, Source, bool) {
	if _, found := Lookup(canonicalName); !found {
		panic(fmt.Sprintf("envvars.LookupEnv: unknown name %q (not in registry)", canonicalName))
	}
	if v, ok := os.LookupEnv(canonicalName); ok {
		return v, SourceABCEnv, true
	}
	return "", SourceUnset, false
}

// Get returns the value of the canonical env var (empty when unset).
// Equivalent to os.Getenv but validates the name against the registry.
func Get(canonicalName string) string {
	v, _, _ := LookupEnv(canonicalName)
	return v
}

// IsSet reports whether the canonical name is set in the environment
// (LookupEnv-style: explicit empty counts as set).
func IsSet(canonicalName string) bool {
	_, _, ok := LookupEnv(canonicalName)
	return ok
}

// IsTruthy returns true when the value is one of the standard truthy
// strings (1 / true / yes / y / on, case-insensitive). Useful for
// boolean-flag env vars like ABC_CLI_DEBUG.
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
