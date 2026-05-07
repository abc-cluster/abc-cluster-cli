// Package accounting provides the rate-card resolver used by the
// `abc accounting` and `abc emissions` reporting verbs.
//
// Three layers of override (Layer 2 wins):
//
//	Layer 0 — hardcoded SA on-prem indicative constants (defaults_za.go)
//	Layer 1 — per-context accounting:/emissions: blocks in ~/.abc/config.yaml
//	Layer 2 — per-invocation flags (e.g. --rate-cpu-hour=, --pue=)
//
// At resolve time every rate field carries a (value, source, updated_at,
// citation) tuple. Reports surface this as a "Rate card (effective)" footer.
//
// See specs/active/abc-emissions-accounting.md and
// brainstorms/emissions-accounting/2026-05-07-config-rate-card-design.md.
package accounting

import "time"

// Source enumerates where a rate value came from at resolve time.
type Source string

const (
	SourceBuiltIn Source = "built-in"
	SourceConfig  Source = "config"
	SourceFlag    Source = "flag"
	// SourceAbcCloud is reserved for Phase 2 cloud-published rate cards.
	SourceAbcCloud Source = "abc-cloud"
)

// RateValue carries a numeric rate with provenance.
type RateValue struct {
	Value     float64
	Source    Source
	UpdatedAt time.Time
	Citation  string
}

// RateString carries a string-valued rate (currently just currency) with
// provenance, mirroring RateValue's shape.
type RateString struct {
	Value     string
	Source    Source
	UpdatedAt time.Time
	Citation  string
}

// CostRates is the cost section of a RateCard.
//
// Storage cost splits cleanly into two attribution shapes — see
// brainstorms/emissions-accounting/2026-05-07-storage-accounting.md:
//   • scratch (transient, per-run, GB·hour)
//   • persistent (project-scoped, GB·month)
type CostRates struct {
	CpuHour      RateValue
	GpuHour      RateValue
	MemoryGbHour RateValue
	// Storage — used by reports when runs.scratch_gb is populated and (for
	// persistent) when project_storage_snapshots rows exist.
	StorageScratchGbHour     RateValue
	StoragePersistentGbMonth RateValue
	// StorageEgressGb is non-zero only when an external cross-boundary
	// transfer happens (cloud bridge); on-prem reports leave it at zero.
	StorageEgressGb RateValue
}

// EmissionsRates is the emissions section of a RateCard.
type EmissionsRates struct {
	GridFactorGco2PerKwh RateValue
	CpuW                 RateValue
	GpuW                 RateValue
	MemoryGbW            RateValue
	Pue                  RateValue
	// Storage emissions — split mirrors CostRates. The amplification
	// factor captures erasure-coding overhead (3+1 default → 1.33×) so
	// future configurations override it without re-deriving per-drive
	// wattage.
	StorageScratchWPerTb    RateValue
	StoragePersistentWPerTb RateValue
	StorageEcAmplification  RateValue
}

// RateCard is the resolved set of rates for a (context, invocation) pair.
type RateCard struct {
	Currency  RateString
	Cost      CostRates
	Emissions EmissionsRates
}
