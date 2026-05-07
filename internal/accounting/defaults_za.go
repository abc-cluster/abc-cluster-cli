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

	// Emissions coefficients.
	ZADefaultGridFactorGco2PerKwh = 900.0   // Eskom IAR 2023 average (g CO2e / kWh)
	ZADefaultCpuW                 = 12.0    // CCF v3 coefficient (W per CPU)
	ZADefaultGpuW                 = 250.0   // CCF v3 coefficient (W per GPU)
	ZADefaultMemoryGbW            = 0.3725  // CCF v3 coefficient (W per GB DRAM)
	ZADefaultPue                  = 1.5     // generic on-prem average
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
			// Storage + egress are reserved (Phase 2). Not used by reports.
			StorageGbMonth: RateValue{Source: SourceBuiltIn, UpdatedAt: now},
			EgressGb:       RateValue{Source: SourceBuiltIn, UpdatedAt: now},
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
		},
	}
}
