// ratecard.go — thin helper that wires the report verb into the same
// Layer 0/1/2 resolver `abc accounting` and `abc emissions` use. Spec
// abc-report.md §D' (BINDING): the report's spend, emissions, and
// postdoc-rate translation must come from one resolver, not parallel
// hardcoded constants. Drift between the three verbs is a regression.
//
// We intentionally do NOT accept Layer-2 flag overrides for `abc report`
// in v1 — the verb has no rate-override flags. Layer 0 + Layer 1 are
// sufficient for the headline ROI line; if a user wants to pin a
// specific rate, they go through `abc config accounting set …` (Layer 1).
package report

import (
	"fmt"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
)

// LoadRateCard resolves the active rate card for the given context. It
// applies Layer 0 (ZADefaults) → Layer 1 (per-context config blocks).
// Layer 2 is left empty: `abc report` has no rate-override flags in v1.
//
// Errors only on invalid Layer 1 values (which the writer would have
// rejected anyway). The caller should treat any error as fatal —
// surfacing it lets the user fix the bad config and re-run.
func LoadRateCard(contextName string) (acct.RateCard, error) {
	layer1, err := acct.LoadLayer1(contextName)
	if err != nil {
		return acct.RateCard{}, fmt.Errorf("read config layer: %w", err)
	}
	card, err := acct.Resolve(acct.ZADefaults(), layer1, acct.FlagOverrides{})
	if err != nil {
		return acct.RateCard{}, err
	}
	return card, nil
}

// CostPerRun computes the ZAR cost of a single run from the resolved
// rate card. Mirrors the same formula `acct.Aggregate(ModeAccounting)`
// applies, so per-run rollups stay self-consistent with the windowed
// totals abc accounting reports.
//
//   cost = cpu_hours * cpu_per_hour
//        + memory_gb_hours * mem_per_hour
//        + gpu_count * walltime_hours * gpu_per_hour
//        + scratch_gb * walltime_hours * scratch_per_hour_gb
func CostPerRun(card acct.RateCard, cpuHours, memGbHours float64, gpuCount int64, walltimeHours float64, scratchGb float64) float64 {
	gpuHours := float64(gpuCount) * walltimeHours
	scratchGbHours := scratchGb * walltimeHours
	return cpuHours*card.Cost.CpuHour.Value +
		memGbHours*card.Cost.MemoryGbHour.Value +
		gpuHours*card.Cost.GpuHour.Value +
		scratchGbHours*card.Cost.StorageScratchGbHour.Value
}

// EmissionsPerRunKg computes the kg CO₂e of a single run from the
// resolved rate card. Mirrors `acct.Aggregate(ModeEmissions)` exactly.
//
//   energy_kwh = ((cpu_hours * cpu_w + gpu_hours * gpu_w + mem_gb_hours * mem_gb_w) / 1000) * pue
//              + (scratch_gb_hours * scratch_w_per_tb / 1000 / 1000) * pue
//   kg_co2e   = energy_kwh * grid_factor_g_per_kwh / 1000
func EmissionsPerRunKg(card acct.RateCard, cpuHours, memGbHours float64, gpuCount int64, walltimeHours float64, scratchGb float64) float64 {
	gpuHours := float64(gpuCount) * walltimeHours
	scratchGbHours := scratchGb * walltimeHours
	em := card.Emissions
	scratchEnergyKwh := scratchGbHours * em.StorageScratchWPerTb.Value / 1000.0 / 1000.0
	energyKwh := ((cpuHours*em.CpuW.Value +
		gpuHours*em.GpuW.Value +
		memGbHours*em.MemoryGbW.Value) / 1000.0) * em.Pue.Value
	energyKwh += scratchEnergyKwh * em.Pue.Value
	return energyKwh * em.GridFactorGco2PerKwh.Value / 1000.0
}
