package capability

import (
	"os"
	"strconv"
	"time"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// Default TTL knobs per OQ-CAP-1 resolution. Overridable via env.
const (
	defaultForegroundTTL = 10 * time.Minute
	defaultHardExpiry    = 24 * time.Hour
)

// foregroundTTL returns the foreground TTL, honouring ABC_CAPABILITY_TTL.
func foregroundTTL() time.Duration {
	if v := os.Getenv("ABC_CAPABILITY_TTL"); v != "" {
		if mins, err := strconv.Atoi(v); err == nil && mins >= 0 {
			return time.Duration(mins) * time.Minute
		}
	}
	return defaultForegroundTTL
}

// hardExpiry returns the hard expiry, honouring ABC_CAPABILITY_HARD_EXPIRY.
func hardExpiry() time.Duration {
	if v := os.Getenv("ABC_CAPABILITY_HARD_EXPIRY"); v != "" {
		if hrs, err := strconv.Atoi(v); err == nil && hrs >= 0 {
			return time.Duration(hrs) * time.Hour
		}
	}
	return defaultHardExpiry
}

// probeDisabled returns true when the user has set ABC_NO_PROBE=1.
// Verbs that respect this still operate against the cached / tier-default
// capability map; just no probe is attempted, foreground or background.
func probeDisabled() bool {
	return os.Getenv("ABC_NO_PROBE") == "1"
}

// Fresh returns the freshness state for a stored Capabilities block.
// Intended for the per-invocation decision: serve from cache, kick off
// background revalidation, or block on a foreground probe.
//
// Returns FirstRun when caps is nil or LastSynced is zero — the caller
// should run with tier-default assumptions and surface a one-line
// "run 'abc cluster capabilities sync'" nudge.
func Fresh(caps *cfg.Capabilities) Freshness {
	if probeDisabled() {
		// Treat the cache as fresh regardless of age. The user has
		// opted out of probing; we serve what we have.
		return FreshCache
	}
	if caps == nil || caps.LastSynced.IsZero() {
		return FirstRun
	}
	age := time.Since(caps.LastSynced)
	switch {
	case age < foregroundTTL():
		return FreshCache
	case age < hardExpiry():
		return RevalidateInBg
	default:
		return BlockingProbe
	}
}
