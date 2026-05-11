package config

import (
	"fmt"
	"strings"
)

// Cluster tier values (OSS-1 / OSS-2 / commercial) per ABC platform vision.
//
// Naming history:
//   - 2026-05-03: original product names `abc-nodes` / `abc-cluster` / `abc-cloud`
//                 superseded by `abc-seed` / `abc-grove` / `abc-cloud`.
//   - 2026-05-08: `abc-seed` further renamed to `abc-seedling`. Grove and cloud
//                 names unchanged. The internal codename `garden` (for cloud)
//                 stays internal-only.
//
// Canonical strings (what NormalizeClusterType returns and what the CLI persists
// to ~/.abc/config.yaml):
const (
	ClusterTypeABCSeedling = "abc-seedling" // canonical OSS-1 (seedling tier)
	ClusterTypeABCGrove    = "abc-grove"    // canonical OSS-2 (grove tier)
	ClusterTypeABCCloud    = "abc-cloud"    // canonical commercial (cloud tier; codename `garden` is internal-only)
)

// Backward-compatible aliases for callers that still reference the older
// constant names by symbol. Both name and value resolve to the new canonical.
//
// New code should prefer ClusterTypeABCSeedling / ClusterTypeABCGrove.
const (
	ClusterTypeABCNodes   = ClusterTypeABCSeedling // was "abc-nodes"
	ClusterTypeABCCluster = ClusterTypeABCGrove    // was "abc-cluster" (OSS-2 product name; superseded 2026-05-03)
)

// Legacy alias strings accepted on read (config files, --cluster-type flag).
// Each maps to a canonical above. Kept private — call sites should normalize
// via NormalizeClusterType rather than compare against these directly.
const (
	legacyClusterTypeABCNode    = "abc-node"     // singular, original
	legacyClusterTypeABCNodes   = "abc-nodes"    // plural, pre-2026-05-03
	legacyClusterTypeABCSeed    = "abc-seed"     // 2026-05-03 → 2026-05-08
	legacyClusterTypeABCSeedT   = "abc-seed-tended"
	legacyClusterTypeABCCluster = "abc-cluster"  // OSS-2 product name pre-2026-05-03
)

// NormalizeClusterType resolves any of the accepted alias strings (current and
// legacy, in either short `abc-X` or long `abc-cluster-X` form, with or without
// the `-tended` sub-tier suffix) to the canonical short form.
//
// Whitespace-trimmed and lowercased on entry; `-tended` is treated as the
// same tier (the cluster_type field declares product tier identity, not the
// service-set "tended" status).
//
// Returns (canonical, true) on a known input; ("", false) on anything else.
func NormalizeClusterType(s string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(s)) {

	// ── Seedling tier (OSS-1) ────────────────────────────────────────────
	case ClusterTypeABCSeedling,
		"abc-seedling-tended",
		"abc-cluster-seedling",
		"abc-cluster-seedling-tended",
		legacyClusterTypeABCSeed,
		legacyClusterTypeABCSeedT,
		legacyClusterTypeABCNodes,
		legacyClusterTypeABCNode:
		return ClusterTypeABCSeedling, true

	// ── Grove tier (OSS-2) ───────────────────────────────────────────────
	case ClusterTypeABCGrove,
		"abc-grove-tended",
		"abc-cluster-grove",
		"abc-cluster-grove-tended",
		legacyClusterTypeABCCluster:
		return ClusterTypeABCGrove, true

	// ── Cloud tier (commercial; codename `garden` internal-only) ─────────
	case ClusterTypeABCCloud,
		"abc-cluster-cloud",
		"abc-garden",
		"abc-cluster-garden":
		return ClusterTypeABCCloud, true

	default:
		return "", false
	}
}

// PersistActiveContextClusterType sets contexts.<active>.cluster_type after
// normalising the input. Stored value is always the canonical short form.
func PersistActiveContextClusterType(tier string) error {
	norm, ok := NormalizeClusterType(tier)
	if !ok {
		return fmt.Errorf(
			"invalid cluster_type %q (want one of: %s, %s, %s — plus -tended / abc-cluster-… variants)",
			tier, ClusterTypeABCSeedling, ClusterTypeABCGrove, ClusterTypeABCCloud,
		)
	}
	cfg, err := Load()
	if err != nil {
		return err
	}
	name := strings.TrimSpace(cfg.ActiveContext)
	if name == "" {
		return fmt.Errorf("no active_context set; run: abc context use <name>")
	}
	if !cfg.HasDefinedContext(name) {
		return fmt.Errorf("no saved context %q", name)
	}
	canon := cfg.ResolveContextName(name)
	ctx := cfg.Contexts[canon]
	ctx.ClusterType = norm
	cfg.Contexts[canon] = ctx
	return cfg.Save()
}
