package capability

import (
	"fmt"
	"strings"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// Require evaluates a verb's Required declaration against the resolved
// Capabilities map and returns a Decision telling the verb how to
// proceed.
//
// flagsSet is the set of user-passed flags (true when the user set the
// flag, false otherwise). OptionalFor[flag] needs only matter when the
// flag is true.
//
// Semantics (per the brainstorm):
//   - AllOf: every Need must be satisfied. Any unmet → FailReason set.
//   - AnyOf: at least one Need must be satisfied. The FIRST listed Need
//     that's satisfied becomes Decision.Backend (so callers list AnyOf
//     options best-to-worst). If multiple match and the chosen isn't
//     the first listed best option, Degraded is set with a reason.
//   - OptionalFor[flag]: when flag is set, the Need must be satisfied;
//     unmet flags appear in UnusableFlags.
//
// Banner is composed from the degradation reason (if any). UnusableFlags
// triggers a hard-error rendering — verbs should call .AsError() and
// return immediately.
func Require(decl Required, caps *cfg.Capabilities, flagsSet map[string]bool) Decision {
	d := Decision{}

	// AllOf — every entry must be satisfied; otherwise fail.
	for _, n := range decl.AllOf {
		if !satisfies(n, caps) {
			d.FailReason = fmt.Sprintf(
				"required: %s >= %s%s; not available",
				FormatService(n.Service), n.MinVersion, formatFeatures(n.Features),
			)
			return d
		}
	}

	// AnyOf — pick the first that satisfies. Degraded if the chosen
	// one isn't the first listed (callers list best-first).
	if len(decl.AnyOf) > 0 {
		var chosenIdx = -1
		for i, n := range decl.AnyOf {
			if satisfies(n, caps) {
				chosenIdx = i
				break
			}
		}
		if chosenIdx < 0 {
			// None matched.
			labels := make([]string, len(decl.AnyOf))
			for i, n := range decl.AnyOf {
				labels[i] = FormatService(n.Service) + " >= " + n.MinVersion + formatFeatures(n.Features)
			}
			d.FailReason = "no acceptable backend; need one of: " + strings.Join(labels, "; ")
			return d
		}
		d.Backend = decl.AnyOf[chosenIdx].Service
		if chosenIdx > 0 {
			d.Degraded = true
			preferred := decl.AnyOf[0]
			d.DegradeReason = fmt.Sprintf(
				"%s not available; running against %s instead",
				FormatService(preferred.Service), FormatService(d.Backend),
			)
			d.Banner = "[abc] note: " + d.DegradeReason
			// If the chosen backend has a fallback hint, append it.
			if caps != nil && caps.Services != nil {
				if sc, ok := caps.Services[d.Backend]; ok && sc.Fallback != "" {
					d.Banner += " (" + sc.Fallback + ")"
				}
			}
		}
	}

	// OptionalFor[flag] — only matters when the flag is set.
	for flag, n := range decl.OptionalFor {
		if !flagsSet[flag] {
			continue
		}
		if !satisfies(n, caps) {
			d.UnusableFlags = append(d.UnusableFlags, FlagBlocked{
				Flag:         flag,
				RequiredNeed: n,
				Reason:       fmt.Sprintf("%s >= %s%s not available", FormatService(n.Service), n.MinVersion, formatFeatures(n.Features)),
			})
		}
	}

	return d
}

// AsError formats a Decision as a user-facing error when the verb
// cannot run. Returns nil when the decision is acceptable.
func (d Decision) AsError() error {
	if !d.Failed() {
		return nil
	}
	var b strings.Builder
	if d.FailReason != "" {
		b.WriteString("cannot run on this cluster: ")
		b.WriteString(d.FailReason)
	}
	if len(d.UnusableFlags) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		for i, fb := range d.UnusableFlags {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "--%s requires %s; %s",
				fb.Flag, FormatService(fb.RequiredNeed.Service), fb.Reason)
		}
	}
	return fmt.Errorf("%s", b.String())
}

// satisfies reports whether the resolved Capabilities meet a single Need.
//
// Special-case: "local-state" is a pseudo-service per OQ-CAP-8 — its
// version is the highest applied migration name; its features are
// derived from the migration→features table (see
// internal/state/migrations/features.go in Stage A scope, or hardcoded
// list when that lands).
func satisfies(n Need, caps *cfg.Capabilities) bool {
	if caps == nil || caps.Services == nil {
		// Special-case: local-state is always notionally available
		// even when the Services map is empty (e.g. brand-new install
		// before the first sync). The version comparison still applies.
		if n.Service == "local-state" {
			return localStateSatisfies(n)
		}
		return false
	}
	sc, ok := caps.Services[n.Service]
	if !ok {
		// local-state is implicit — derive from local migrations when
		// not present in the Services map at all (fresh install / first
		// run before sync). An explicit entry — even Available=false —
		// is honoured below.
		if n.Service == "local-state" {
			return localStateSatisfies(n)
		}
		return false
	}
	if !sc.Available {
		return false
	}
	if !versionAtLeast(sc.Version, n.MinVersion) {
		return false
	}
	for _, f := range n.Features {
		if !contains(sc.Features, f) {
			return false
		}
	}
	return true
}

// versionAtLeast returns true when have >= want under lexicographic
// comparison. Sufficient for both semver-shaped strings ("0.5.0") and
// migration-name-shaped strings ("0005_rate_card_revisions").
func versionAtLeast(have, want string) bool {
	if want == "" {
		return true
	}
	return have >= want
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func formatFeatures(features []string) string {
	if len(features) == 0 {
		return ""
	}
	return " with feature(s) [" + strings.Join(features, ", ") + "]"
}
