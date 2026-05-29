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
	// SourceBuiltIn — shipped with the binary, immutable, trusted for
	// canonical reports.
	SourceBuiltIn Source = "built-in"
	// SourceLocal — overridden in ~/.abc/config.yaml. Per the permissions
	// model (design/exploring/permissions-accounting.md)
	// these values are ADVISORY ONLY: a future `--signed` report will refuse
	// to sign if any field resolves to SourceLocal because the user's local
	// override is not reproducible by anyone else.
	SourceLocal Source = "local"
	// SourceFlag — overridden by a per-invocation flag. Same advisory-only
	// status as SourceLocal.
	SourceFlag Source = "flag"
	// SourceAbcCloud is reserved for Phase 2 cloud-published rate cards.
	SourceAbcCloud Source = "abc-cloud"
)

// SourceConfig is the legacy alias for SourceLocal — kept so older callsites
// that haven't been renamed continue to compile. New code should use
// SourceLocal directly.
const SourceConfig = SourceLocal

// IsAdvisory reports whether a Source is locally-overridable and therefore
// not authoritative for canonical (signed) reports.
func (s Source) IsAdvisory() bool {
	return s == SourceLocal || s == SourceFlag
}

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
	// PostdocPerHour is the hourly compensation rate used by `abc report`
	// to translate `hours_saved` into a currency amount. Layer-0 default
	// is the HSRC 2025 South African postdoctoral guidance figure.
	// Surfaced through the same resolver chain as the other cost rates so
	// the report verb's footer matches `abc accounting`'s shape.
	PostdocPerHour RateValue
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

	// Water Usage Effectiveness (WUE) — added 2026-05-29.
	// Formula: Water (L) = energy_kWh × (WueSite + GridWaterIntensity)
	//
	// WueSite is the facility-level direct cooling evaporation: litres of
	// freshwater evaporated per kWh of IT load.  For Cape Town on-prem with
	// evaporative cooling the indicative range is 1.2–2.0 L/kWh.
	//
	// GridWaterIntensity (I_water) is the indirect grid water footprint: litres
	// consumed per kWh delivered at the meter from the regional grid mix.
	// For Eskom's coal-heavy grid, cooling tower evaporation at thermal
	// stations contributes ~2.0–3.0 L/kWh.
	//
	// References: The Green Grid WUE whitepaper (2012); SA water brainstorm
	// brainstorms/water-carbon-scheduling/2026-05-29-cue-wue-aware-scheduling.md.
	WueSite             RateValue
	GridWaterIntensity  RateValue
}

// RateCard is the resolved set of rates for a (context, invocation) pair.
type RateCard struct {
	Currency  RateString
	Cost      CostRates
	Emissions EmissionsRates
}
