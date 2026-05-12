package envvars

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Source identifies where a resolved value came from. Useful for
// debugging ("why is the wrong cluster being hit?") via `abc env show`.
type Source int

const (
	// SourceUnset: no value found at any layer; Default returned.
	SourceUnset Source = iota
	// SourceFlag: cmd.Flags().Changed(name) was true; flag value won.
	SourceFlag
	// SourceABCEnv: canonical ABC env var was set (via os.LookupEnv).
	SourceABCEnv
	// SourceVendorEnv: vendor fallback was set (NOMAD_*, VAULT_*, ...).
	SourceVendorEnv
	// SourceContext: value came from the active context config.
	SourceContext
	// SourceDefault: Entry.Default returned because no other layer matched.
	SourceDefault
)

// String returns a stable identifier for the source (used in
// `abc env show` output and in tests).
func (s Source) String() string {
	switch s {
	case SourceUnset:
		return "unset"
	case SourceFlag:
		return "flag"
	case SourceABCEnv:
		return "abc-env"
	case SourceVendorEnv:
		return "vendor-env"
	case SourceContext:
		return "context"
	case SourceDefault:
		return "default"
	}
	return "unknown"
}

// FlagLookup reports whether cobra (or any flag set) saw an explicit value
// for name, and returns that value. The function MUST honour Cobra's
// `cmd.Flags().Changed(name)` semantics: a flag that was not set on the
// command line returns ok=false even if it has a non-empty default.
type FlagLookup func(name string) (value string, ok bool)

// ContextLookup reports whether the active context has a value for the
// given key, and returns it. Keys match Entry.ContextKey ("url",
// "access_token", "workspace_id", "region", "namespace", "org_id",
// "output_format").
type ContextLookup func(key string) (value string, ok bool)

// EnvLookup is the abstraction over os.LookupEnv. Production callers use
// OSEnv; tests pass MapEnv to control the environment.
type EnvLookup func(name string) (value string, ok bool)

// OSEnv is the default EnvLookup, delegating to os.LookupEnv.
//
// CRITICAL: must use LookupEnv (not Getenv) so that an explicit empty
// value (e.g. `ABC_API_ADDRESS=` on the command line) is distinguishable from
// an unset variable. An explicit empty must override context config.
func OSEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}

// MapEnv is an EnvLookup backed by a map. Useful in tests.
func MapEnv(m map[string]string) EnvLookup {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

// NoFlag is a FlagLookup that always reports no flag set. Use when the
// caller is not a Cobra command (e.g. background workers, init paths).
var NoFlag FlagLookup = func(string) (string, bool) { return "", false }

// NoContext is a ContextLookup that always reports no context value. Use
// when the active context is not loaded (e.g. `abc auth login` flows).
var NoContext ContextLookup = func(string) (string, bool) { return "", false }

// Resolver bundles the three lookup functions plus warning sink and
// session-warning de-duplication state.
type Resolver struct {
	Flag    FlagLookup
	Env     EnvLookup
	Context ContextLookup

	// WarnSink receives one-time warnings (vendor fallback only — pre-1.0
	// canonical-only registry has no alias deprecation warnings).
	// Defaults to os.Stderr when nil.
	WarnSink io.Writer

	// HasABCContext is consulted before emitting a vendor-fallback warning.
	// When true, the resolver assumes the user knows what they're doing
	// (they have a context configured) and does not nag about NOMAD_ADDR
	// etc. that may simply be leftover shell state.
	HasABCContext bool

	mu       sync.Mutex
	warnedAt map[string]struct{}
}

// New constructs a Resolver with sensible production defaults.
func New(flag FlagLookup, ctx ContextLookup) *Resolver {
	return &Resolver{
		Flag:    flag,
		Env:     OSEnv,
		Context: ctx,
	}
}

// warnOnce emits a stderr warning if key has not been warned for in this
// resolver's lifetime. Safe for concurrent callers.
func (r *Resolver) warnOnce(key, msg string) {
	r.mu.Lock()
	if r.warnedAt == nil {
		r.warnedAt = map[string]struct{}{}
	}
	if _, seen := r.warnedAt[key]; seen {
		r.mu.Unlock()
		return
	}
	r.warnedAt[key] = struct{}{}
	r.mu.Unlock()

	sink := r.WarnSink
	if sink == nil {
		sink = os.Stderr
	}
	fmt.Fprintln(sink, msg)
}

// Resolve walks the precedence ladder for the given canonical Entry name
// and returns the value plus the Source that won.
//
// Precedence (highest first):
//  1. flag                  — cmd.Flags().Changed(FlagName) && FlagLookup(FlagName)
//  2. canonical ABC env     — Env(Entry.Name)         (via os.LookupEnv)
//  3. vendor fallback env   — Env(Entry.VendorFallback)        (warns once if no ABC context)
//  4. active context        — Context(Entry.ContextKey)
//  5. default               — Entry.Default
//
// Returns ("", SourceUnset, error) when name is not in the registry.
func (r *Resolver) Resolve(name string) (string, Source, error) {
	entry, ok := Lookup(name)
	if !ok {
		return "", SourceUnset, fmt.Errorf("envvars: unknown variable %q (not in registry)", name)
	}

	// 1. Flag
	if r.Flag != nil && entry.FlagName != "" {
		if v, ok := r.Flag(entry.FlagName); ok {
			return v, SourceFlag, nil
		}
	}

	// 2. Canonical ABC env (LookupEnv: honour explicit empty)
	if r.Env != nil {
		if v, ok := r.Env(entry.Name); ok {
			return v, SourceABCEnv, nil
		}
		// 3. Vendor fallback (warn once when no ABC context)
		if entry.VendorFallback != "" {
			if v, ok := r.Env(entry.VendorFallback); ok {
				if !r.HasABCContext {
					r.warnOnce("vendor:"+entry.VendorFallback,
						fmt.Sprintf("warning: using %s; prefer %s or 'abc auth context add'",
							entry.VendorFallback, entry.Name))
				}
				return v, SourceVendorEnv, nil
			}
		}
	}

	// 4. Active context config
	if r.Context != nil && entry.ContextKey != "" {
		if v, ok := r.Context(entry.ContextKey); ok {
			return v, SourceContext, nil
		}
	}

	// 5. Default
	if entry.Default != "" {
		return entry.Default, SourceDefault, nil
	}
	return "", SourceUnset, nil
}

// MustResolve is Resolve that panics on unknown names. Use in init paths
// where the name is a registry constant and "unknown" is a programmer
// error.
func (r *Resolver) MustResolve(name string) (string, Source) {
	v, s, err := r.Resolve(name)
	if err != nil {
		panic(err)
	}
	return v, s
}

// Get is a convenience that discards the Source.
func (r *Resolver) Get(name string) string {
	v, _, _ := r.Resolve(name)
	return v
}
