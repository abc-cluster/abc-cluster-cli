// Package water implements `abc report water` — freshwater consumption
// reporting using the Program WUE formula from The Green Grid.
//
// # Consolidation (done 2026-06-05)
//
// water and emissions (cmd/emissions) were folded from top-level verbs under
// `abc report` on 2026-06-05:
//
//	abc report water       (was: abc water)
//	abc report emissions   (was: abc emissions)
//
// abc accounting stayed a top-level verb — it is --cloud budget management,
// not a read-only report. See brainstorm
// cli-ux-harmonization/2026-06-05-command-surface-review.md.
//
// Formula:
//
//	Water (L) = energy_kWh × (wue_site + grid_water_intensity)
//
// where energy_kWh uses the same CCF v3 coefficients as `abc emissions`, so
// the two reports are always consistent on energy while applying their own
// environmental dimension (CO₂e vs freshwater litres).
//
// Rate card configuration lives in the emissions block of config.yaml — the
// two new WUE keys are siblings of the carbon coefficients:
//
//	abc config emissions set wue_site=1.5
//	abc config emissions set grid_water_intensity=2.5
//
// Built-in defaults (Layer 0) reflect Cape Town on-prem with Eskom coal:
//   - wue_site            = 1.5 L/kWh  (evaporative cooling midpoint)
//   - grid_water_intensity = 2.5 L/kWh  (Eskom coal thermal I_water)
//
// Overriding for cross-node comparison (see brainstorm for per-site values):
//
//	abc water --wue-site=0.2 --grid-water-intensity=0.9   # Belgium / KU Leuven
//	abc water --wue-site=0.5 --grid-water-intensity=15.0  # Kenya hydro estimate
//
// No network calls — everything is read from ~/.abc/local.db.
package water

import (
	"context"
	"fmt"
	"os"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// NewCmd returns the water-report command, mounted as `abc report water`.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "water",
		Aliases: []string{"wue"},
		Short:   "Freshwater consumption report — reads from ~/.abc/local.db",
		Long: `Report freshwater consumption for jobs tracked in the local runs table.

Uses the Program WUE formula (The Green Grid, 2012):

  Water (L) = energy_kWh × (wue_site + grid_water_intensity)

where energy_kWh is computed from CPU·hours, GPU·hours, and memory GB·hours
using the same CCF v3 coefficients as ` + "`abc emissions`" + ` — both reports
draw from the same energy base so the numbers are always consistent.

Rate-card override chain (Layer 2 wins):
  Layer 0 — built-in Cape Town / Eskom coal defaults
               wue_site = 1.5 L/kWh   (evaporative cooling estimate)
               grid_water_intensity = 2.5 L/kWh  (Eskom I_water)
  Layer 1 — ~/.abc/config.yaml  (abc config emissions set wue_site=...)
  Layer 2 — per-invocation flags (--wue-site=, --grid-water-intensity=)

Cross-node comparison examples:
  abc report water --wue-site=0.2 --grid-water-intensity=0.9   # Belgium / KU Leuven
  abc report water --wue-site=0.5 --grid-water-intensity=15    # Kenya hydro estimate
  abc report water --wue-site=1.8 --grid-water-intensity=3.0   # SA summer peak

Setting permanent overrides for your facility:
  abc config emissions set wue_site=1.27 grid_water_intensity=2.3

Full per-node estimates:
  brainstorms/water-carbon-scheduling/2026-05-29-cue-wue-aware-scheduling.md

Examples:
  abc report water
  abc report water --by=project --since=2026-01-01 --unit=m3
  abc report water --by=namespace --output=csv > q1-2026-water.csv`,
		Args: cobra.NoArgs, // flag-only; reject stray verbs like `water report`
		RunE: runWater,
	}

	cmd.Flags().String("by", "namespace",
		"Aggregation axis: namespace|project|investigation|user|pipeline")
	cmd.Flags().String("since", "",
		"Report window start (YYYY-MM-DD); default: Jan 1 of current year")
	cmd.Flags().String("until", "",
		"Report window end (YYYY-MM-DD); default: now")
	cmd.Flags().String("unit", "L",
		"Display unit: L (litres, default) | mL | m3")
	cmd.Flags().String("output", "table",
		"Output format: table (default) | csv | json")
	cmd.Flags().String("rate-source", "full",
		"Rate card footer verbosity: full (default) | brief | none")
	cmd.Flags().Bool("include-incomplete", false,
		"Include running jobs (cost-so-far; resource figures may be zero)")

	// Layer 2 flag overrides for WUE coefficients.
	cmd.Flags().Float64("wue-site", 0,
		"Override wue_site (direct cooling evaporation, L/kWh) for this invocation")
	cmd.Flags().Float64("grid-water-intensity", 0,
		"Override grid_water_intensity (grid I_water, L/kWh) for this invocation")
	// Also allow overriding the shared energy coefficients (same as abc emissions).
	cmd.Flags().Float64("pue", 0,
		"Override PUE for this invocation")

	return cmd
}

func runWater(cmd *cobra.Command, _ []string) error {
	since, until, err := resolveWindow(cmd)
	if err != nil {
		return err
	}

	byStr, _ := cmd.Flags().GetString("by")
	by, err := parseGroupBy(byStr)
	if err != nil {
		return err
	}

	unitStr, _ := cmd.Flags().GetString("unit")
	unit, err := parseWaterUnit(unitStr)
	if err != nil {
		return err
	}

	outputStr, _ := cmd.Flags().GetString("output")
	output, err := parseOutputFormat(outputStr)
	if err != nil {
		return err
	}

	rateSourceStr, _ := cmd.Flags().GetString("rate-source")
	rateSource := acct.RateSourceVerbosity(rateSourceStr)

	includeIncomplete, _ := cmd.Flags().GetBool("include-incomplete")
	contextName := state.ActiveContextName()

	// Resolve rate card: Layer 0 → Layer 1 → Layer 2.
	layer1, err := acct.LoadLayer1(contextName)
	if err != nil {
		return fmt.Errorf("load rate card: %w", err)
	}
	layer2 := buildWaterLayer2(cmd)
	card, err := acct.Resolve(acct.ZADefaults(), layer1, layer2)
	if err != nil {
		return fmt.Errorf("resolve rate card: %w", err)
	}

	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local state: %w", err)
	}
	defer db.Close()

	opts := acct.ReportOptions{
		Mode:              acct.ModeWater,
		By:                by,
		Since:             since,
		Until:             until,
		ContextName:       contextName,
		IncludeIncomplete: includeIncomplete,
		Output:            output,
		RateSource:        rateSource,
		WaterUnit:         unit,
	}

	rep, err := acct.Aggregate(context.Background(), db, opts, card)
	if err != nil {
		return fmt.Errorf("aggregate: %w", err)
	}
	return acct.Render(cmd.OutOrStdout(), rep, opts)
}

// ---- helpers ----------------------------------------------------------------

func resolveWindow(cmd *cobra.Command) (since, until time.Time, err error) {
	now := time.Now()
	until = now

	sinceStr, _ := cmd.Flags().GetString("since")
	untilStr, _ := cmd.Flags().GetString("until")

	if sinceStr != "" {
		since, err = time.ParseInLocation("2006-01-02", sinceStr, time.Local)
		if err != nil {
			return since, until, fmt.Errorf("--since: %w", err)
		}
	} else {
		since = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.Local)
	}
	if untilStr != "" {
		until, err = time.ParseInLocation("2006-01-02", untilStr, time.Local)
		if err != nil {
			return since, until, fmt.Errorf("--until: %w", err)
		}
		until = until.Add(24*time.Hour - time.Second)
	}
	return since, until, nil
}

func parseGroupBy(s string) (acct.GroupBy, error) {
	switch s {
	case "namespace", "":
		return acct.GroupByNamespace, nil
	case "project":
		return acct.GroupByProject, nil
	case "investigation":
		return acct.GroupByInvestigation, nil
	case "user":
		return acct.GroupByUser, nil
	case "pipeline":
		return acct.GroupByPipeline, nil
	}
	return "", fmt.Errorf("--by=%q not supported (allowed: namespace, project, investigation, user, pipeline)", s)
}

func parseWaterUnit(s string) (acct.WaterUnit, error) {
	switch s {
	case "L", "l", "":
		return acct.WaterUnitL, nil
	case "mL", "ml":
		return acct.WaterUnitML, nil
	case "m3", "m³":
		return acct.WaterUnitM3, nil
	}
	return "", fmt.Errorf("--unit=%q not supported (allowed: L, mL, m3)", s)
}

func parseOutputFormat(s string) (acct.OutputFormat, error) {
	switch s {
	case "table", "":
		return acct.OutputTable, nil
	case "csv":
		return acct.OutputCSV, nil
	case "json":
		return acct.OutputJSON, nil
	}
	return "", fmt.Errorf("--output=%q not supported (allowed: table, csv, json)", s)
}

func buildWaterLayer2(cmd *cobra.Command) acct.FlagOverrides {
	out := acct.FlagOverrides{Emissions: map[string]string{}}
	if v, _ := cmd.Flags().GetFloat64("wue-site"); v > 0 {
		out.Emissions[acct.KeyWueSite] = fmt.Sprintf("%g", v)
	}
	if v, _ := cmd.Flags().GetFloat64("grid-water-intensity"); v > 0 {
		out.Emissions[acct.KeyGridWaterIntensity] = fmt.Sprintf("%g", v)
	}
	if v, _ := cmd.Flags().GetFloat64("pue"); v > 0 {
		out.Emissions[acct.KeyPue] = fmt.Sprintf("%g", v)
	}
	return out
}

// keep os imported for future use.
var _ = os.Stderr
