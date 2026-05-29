// defaults_za.go — Layer 0 hardcoded South African on-prem indicative
// rate-card constants.
//
// These are *indicative SA on-prem academic compute* values, suitable as
// showback estimates and methods-section defensible numbers for South
// African HDIs. They are NOT invoice-grade. Production showback or
// grant-justification reporting should override via Layer 1 (per-context
// accounting:/emissions: blocks in ~/.abc/config.yaml) or Layer 2
// (per-invocation flags). See spec §B for the citation list.

package accounting

import (
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
)

const (
	// Cost rates — ZAR per unit.
	ZADefaultCostCpuHour      = 0.50 // ZAR per CPU·hour
	ZADefaultCostGpuHour      = 9.00 // ZAR per GPU·hour
	ZADefaultCostMemoryGbHour = 0.05 // ZAR per GB·hour
	ZADefaultCurrency         = "ZAR"

	// Storage cost rates (see brainstorms/emissions-accounting/2026-05-07-storage-accounting.md).
	ZADefaultCostStorageScratchGbHour     = 0.0001 // ZAR per GB·hour, NVMe scratch (capex amortised + power)
	ZADefaultCostStoragePersistentGbMonth = 0.10   // ZAR per GB·month, RustFS HDD JBOD with 3+1 EC
	ZADefaultCostStorageEgressGb          = 0.0    // ZAR per GB egress; non-zero only with cloud bridge

	// Postdoc compensation — used by `abc report` to translate hours_saved
	// into a ZAR amount. Layer-0 default is the HSRC 2025 SA postdoctoral
	// guidance figure (R350/hr).
	ZADefaultCostPostdocPerHour = 350.0 // ZAR per researcher-hour

	// Emissions coefficients.
	ZADefaultGridFactorGco2PerKwh = 900.0  // Eskom IAR 2023 average (g CO2e / kWh)
	ZADefaultCpuW                 = 12.0   // CCF v3 coefficient (W per CPU)
	ZADefaultGpuW                 = 250.0  // CCF v3 coefficient (W per GPU)
	ZADefaultMemoryGbW            = 0.3725 // CCF v3 coefficient (W per GB DRAM)
	ZADefaultPue                  = 1.5    // generic on-prem average

	// Storage emissions coefficients.
	ZADefaultStorageScratchWPerTb    = 8.0  // W per TB, NVMe SSD active+amortised (Samsung PM9A3 envelope)
	ZADefaultStoragePersistentWPerTb = 4.0  // W per TB, HDD idle-dominated (WD Ultrastar DC HC560)
	ZADefaultStorageEcAmplification  = 1.33 // 3+1 erasure coding default; overrideable for replication / wider stripes

	// Water Usage Effectiveness (WUE) coefficients — Cape Town on-prem defaults.
	// Formula: Water (L) = energy_kWh × (WueSite + GridWaterIntensity)
	//
	// WueSite (direct cooling evaporation): 1.5 L/kWh
	//   Midpoint of Cape Town evaporative cooling estimate (1.2–2.0 L/kWh).
	//   Varies ±0.5 L/kWh across the diurnal cycle (wet-bulb temperature
	//   driven). Override with measured facility value for grant reporting.
	//   Source: The Green Grid WUE Measurement Methodology (2012); facility
	//   estimates for Western Cape warm-climate data centres.
	//
	// GridWaterIntensity (indirect grid water, I_water): 2.5 L/kWh
	//   Eskom coal-dominated grid — thermal power plant cooling tower
	//   evaporation. Range 2.0–3.0 L/kWh depending on plant mix and season.
	//   Source: Eskom Sustainability Report 2023; WRI Aqueduct grid-intensity
	//   methodology; SA coal fleet cooling water withdrawal data.
	ZADefaultWueSite            = 1.5 // L/kWh, direct facility cooling evaporation
	ZADefaultGridWaterIntensity = 2.5 // L/kWh, Eskom coal grid indirect I_water
)

// cliReleaseDate is set at build time via -ldflags
// "-X github.com/abc-cluster/abc-cluster-cli/internal/accounting.cliReleaseDate=YYYY-MM-DD".
// Defaults to "unknown" when not injected (go run / unit test builds).
var cliReleaseDate = "unknown"

// CliReleaseDate exposes the build-time release date for use in citation
// strings outside this package (e.g. in tests). Empty string means
// "unknown".
func CliReleaseDate() string {
	if cliReleaseDate == "" {
		return "unknown"
	}
	return cliReleaseDate
}

// dateSuffix returns ", YYYY-MM-DD" when a release date is known, or empty
// string when it isn't. Used to keep the citation strings tidy at "go run"
// build time.
func dateSuffix() string {
	if cliReleaseDate == "" || cliReleaseDate == "unknown" {
		return ""
	}
	return ", " + cliReleaseDate
}

func dateSuffixSemicolon() string {
	if cliReleaseDate == "" || cliReleaseDate == "unknown" {
		return ""
	}
	return "; refreshed " + cliReleaseDate
}

// builtInUpdatedAt returns the UpdatedAt time used for built-in rate values.
// Where the release date is known, it parses YYYY-MM-DD; otherwise returns
// the zero Time, which renders as a placeholder in the report footer.
func builtInUpdatedAt() time.Time {
	if cliReleaseDate == "" || cliReleaseDate == "unknown" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", cliReleaseDate)
	if err != nil {
		return time.Time{}
	}
	return t
}

// costCitation builds the cost-rate citation for a built-in value.
func costCitation() string {
	return "abc-cluster-cli " + state.CLIVersion + " — SA on-prem indicative" + dateSuffix()
}

// gridFactorCitation builds the grid-factor citation for a built-in value.
func gridFactorCitation() string {
	return "Eskom Integrated Annual Report 2023 (Greentech VP doc 2024-09)" + dateSuffixSemicolon()
}

// ccfCitation builds the citation for CPU/GPU/memory wattage coefficients.
func ccfCitation() string {
	return "Cloud Carbon Footprint v3 coefficient set" + dateSuffixSemicolon()
}

// pueCitation builds the citation for the PUE coefficient.
func pueCitation() string {
	return "Uptime Institute 2023 Global Data Center Survey — generic on-prem average" + dateSuffixSemicolon()
}

// storageScratchCostCitation cites the NVMe scratch cost derivation.
func storageScratchCostCitation() string {
	return "amortised enterprise NVMe (4TB SSD ~R8k, 5y, ~6W active) + R3/kWh power" + dateSuffixSemicolon()
}

// storagePersistentCostCitation cites the RustFS HDD JBOD cost derivation.
func storagePersistentCostCitation() string {
	return "RustFS 24-bay HDD JBOD with 3+1 EC, 5y amortisation + power" + dateSuffixSemicolon()
}

// postdocCostCitation builds the citation for the postdoc hourly rate.
// HSRC 2025 South African postdoctoral compensation guidance.
func postdocCostCitation() string {
	return "HSRC 2025 SA postdoctoral compensation guidance" + dateSuffixSemicolon()
}

// storageEgressCitation marks egress as a cross-boundary trigger.
func storageEgressCitation() string {
	return "0 on-prem (Tailscale); set non-zero only when crossing an external boundary"
}

// storageScratchEmissionsCitation cites the NVMe wattage envelope.
func storageScratchEmissionsCitation() string {
	return "Samsung PM9A3 typical-active envelope amortised over capacity (controller + PCIe share included)" + dateSuffixSemicolon()
}

// storagePersistentEmissionsCitation cites the HDD wattage envelope.
func storagePersistentEmissionsCitation() string {
	return "WD Ultrastar DC HC560 idle-dominated (object workload)" + dateSuffixSemicolon()
}

// storageEcCitation cites the erasure-coding amplification default.
func storageEcCitation() string {
	return "RustFS 3+1 erasure coding default; override for replication / wider stripes"
}

// wueSiteCitation cites the direct facility cooling WUE default.
func wueSiteCitation() string {
	return "The Green Grid WUE Methodology (2012); Cape Town evap-cooling midpoint (1.2–2.0 L/kWh)" + dateSuffixSemicolon()
}

// gridWaterIntensityCitation cites the grid I_water default.
func gridWaterIntensityCitation() string {
	return "Eskom Sustainability Report 2023 + WRI Aqueduct; SA coal grid cooling tower evaporation (2.0–3.0 L/kWh)" + dateSuffixSemicolon()
}

// currencyCitation is the citation surfaced for the default currency tag.
func currencyCitation() string {
	return "SA market default"
}

// ZADefaults returns the Layer 0 RateCard for the South African on-prem
// market with every field tagged Source: built-in and the appropriate
// citation strings.
func ZADefaults() RateCard {
	now := builtInUpdatedAt()
	return RateCard{
		Currency: RateString{
			Value:     ZADefaultCurrency,
			Source:    SourceBuiltIn,
			UpdatedAt: now,
			Citation:  currencyCitation(),
		},
		Cost: CostRates{
			CpuHour: RateValue{
				Value: ZADefaultCostCpuHour, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: costCitation(),
			},
			GpuHour: RateValue{
				Value: ZADefaultCostGpuHour, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: costCitation(),
			},
			MemoryGbHour: RateValue{
				Value: ZADefaultCostMemoryGbHour, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: costCitation(),
			},
			StorageScratchGbHour: RateValue{
				Value: ZADefaultCostStorageScratchGbHour, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: storageScratchCostCitation(),
			},
			StoragePersistentGbMonth: RateValue{
				Value: ZADefaultCostStoragePersistentGbMonth, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: storagePersistentCostCitation(),
			},
			StorageEgressGb: RateValue{
				Value: ZADefaultCostStorageEgressGb, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: storageEgressCitation(),
			},
			PostdocPerHour: RateValue{
				Value: ZADefaultCostPostdocPerHour, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: postdocCostCitation(),
			},
		},
		Emissions: EmissionsRates{
			GridFactorGco2PerKwh: RateValue{
				Value: ZADefaultGridFactorGco2PerKwh, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: gridFactorCitation(),
			},
			CpuW: RateValue{
				Value: ZADefaultCpuW, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: ccfCitation(),
			},
			GpuW: RateValue{
				Value: ZADefaultGpuW, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: ccfCitation(),
			},
			MemoryGbW: RateValue{
				Value: ZADefaultMemoryGbW, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: ccfCitation(),
			},
			Pue: RateValue{
				Value: ZADefaultPue, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: pueCitation(),
			},
			StorageScratchWPerTb: RateValue{
				Value: ZADefaultStorageScratchWPerTb, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: storageScratchEmissionsCitation(),
			},
			StoragePersistentWPerTb: RateValue{
				Value: ZADefaultStoragePersistentWPerTb, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: storagePersistentEmissionsCitation(),
			},
			StorageEcAmplification: RateValue{
				Value: ZADefaultStorageEcAmplification, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: storageEcCitation(),
			},
			WueSite: RateValue{
				Value: ZADefaultWueSite, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: wueSiteCitation(),
			},
			GridWaterIntensity: RateValue{
				Value: ZADefaultGridWaterIntensity, Source: SourceBuiltIn, UpdatedAt: now,
				Citation: gridWaterIntensityCitation(),
			},
		},
	}
}
