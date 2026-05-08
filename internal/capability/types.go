// Package capability owns the verb-level requirement DSL plus freshness
// semantics over the Capabilities map stored in ~/.abc/config.yaml.
//
// Stage A scope (per brainstorms/cli-capability-discovery/2026-05-08-...):
//   - Need / Required / Decision / Freshness types (this file)
//   - naming.go    — codename ↔ technical-name table
//   - freshness.go — Fresh() returns FreshCache | RevalidateInBg | BlockingProbe
//   - require.go   — Require(decl, caps, flags) → Decision
//   - mock.go      — MockMap() builder for tests
//
// Pattern:
//
//	var emissionsReportCapabilities = capability.Required{
//	    AnyOf: []capability.Need{
//	        {Service: "local-state",        MinVersion: "0001_initial"},
//	        {Service: "abc-accounting-svc", MinVersion: "0.4.0"},
//	    },
//	    OptionalFor: map[string]capability.Need{
//	        "signed": {Service: "abc-fleet-svc", MinVersion: "0.5.0"},
//	    },
//	}
//
//	decision := capability.Require(emissionsReportCapabilities, caps, flagsSet)
//	if decision.Banner != "" { fmt.Fprintln(os.Stderr, decision.Banner) }
//	if len(decision.UnusableFlags) > 0 {
//	    return decision.AsError()
//	}
//	// proceed with decision.Backend
package capability

// Need is a single requirement against one service.
type Need struct {
	// Service is the technical name (e.g. "abc-fleet-svc"). Match against
	// Capabilities.Services keys; the codename is rendered from naming.go
	// at error / banner time.
	Service string
	// MinVersion is a semver-ish lower bound. For most services this is
	// a semver string ("0.5.0"); for `local-state` it's the migration
	// name ("0005_rate_card_revisions"). Lexicographic comparison gives
	// the right answer in both cases.
	MinVersion string
	// Features (optional) — a service is only acceptable if every
	// feature listed here appears in its Features[] field.
	Features []string
}

// Required is a verb's full capability declaration.
//
// Semantics:
//   - AllOf: every Need must be satisfied. Any failure → fail decision.
//   - AnyOf: at least one Need must be satisfied; the highest-tier match
//     becomes Decision.Backend (highest = first listed when multiple
//     match, by convention list backends best-to-worst).
//   - OptionalFor[flag]: if the user passed `--<flag>`, that Need must
//     be satisfied. If unmet, the flag is added to Decision.UnusableFlags
//     (which the verb surfaces as a hard error — preserves user intent).
//
// All three may be present on the same declaration.
type Required struct {
	AllOf       []Need
	AnyOf       []Need
	OptionalFor map[string]Need
}

// Decision is the return value of Require(). It tells the verb how to
// proceed, what to print, and whether to fail.
type Decision struct {
	// Backend is the technical name of the chosen service (or
	// "local-state" for the local-only path). Empty when fail.
	Backend string
	// Degraded is true when Backend is not the user's preferred
	// (highest-tier) option — e.g. fell back to local-state because
	// abc-accounting-svc was unreachable.
	Degraded bool
	// DegradeReason is human-readable; surfaced via Banner.
	DegradeReason string
	// UnusableFlags lists flags the user passed that can't work given
	// the available capabilities. Triggers a hard error in the verb —
	// `--signed` without Veld is fail-fast, not silent fall-through.
	UnusableFlags []FlagBlocked
	// Banner is the user-facing line printed at the top of stderr when
	// non-empty. Suppressible via ABC_QUIET=1 in the verb's render path.
	Banner string
	// FailReason is set when no AllOf/AnyOf option matched. Empty when
	// the decision is acceptable.
	FailReason string
}

// FlagBlocked names one flag the user passed that can't be honoured.
type FlagBlocked struct {
	Flag         string
	RequiredNeed Need
	Reason       string
}

// Failed reports whether the decision means "verb cannot run".
//
// An empty Backend is NOT itself a failure — AllOf-only declarations
// satisfy without picking a backend (the verb uses all listed
// services). Failure signals are explicit: FailReason set, or
// UnusableFlags non-empty.
func (d Decision) Failed() bool {
	return len(d.UnusableFlags) > 0 || d.FailReason != ""
}

// Freshness describes the stored Capabilities block's age relative to
// the foreground TTL + hard expiry policy.
type Freshness int

const (
	// FreshCache: serve immediately; no probe attempt.
	FreshCache Freshness = iota
	// RevalidateInBg: serve immediately; spawn a background goroutine
	// to re-probe and update the cache for the next invocation.
	RevalidateInBg
	// BlockingProbe: cache too stale (or absent); probe before serving.
	BlockingProbe
	// FirstRun: no Capabilities block yet at all. Verb runs against
	// tier-default assumptions; surfaces a "run sync" nudge.
	FirstRun
)

// String is used in debug logs and `abc cluster capabilities show --explain`.
func (f Freshness) String() string {
	switch f {
	case FreshCache:
		return "fresh"
	case RevalidateInBg:
		return "revalidating-in-background"
	case BlockingProbe:
		return "stale-blocking-probe"
	case FirstRun:
		return "first-run"
	}
	return "unknown"
}
