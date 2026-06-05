// Package emissions implements `abc report emissions` — carbon footprint
// reporting over the local runs table (~/.abc/local.db). Folded under
// `abc report` on 2026-06-05 (was top-level `abc emissions`).
//
// This is the reporting counterpart to `abc config emissions set/show/unset`.
// Rate-card resolution uses the same three-layer chain (Layer 0 SA defaults →
// Layer 1 config.yaml → Layer 2 flags) so `abc emissions` and `abc water`
// always agree on the energy calculation.
//
// No network calls — everything is read from local state.
package emissions

import (
	"context"
	"fmt"
	"os"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// NewCmd returns the emissions-report command, mounted as `abc report emissions`.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "emissions",
		Aliases: []string{"co2", "carbon"},
		Short:   "Carbon footprint report — reads from ~/.abc/local.db",
		Long: `Report CO₂e emissions for jobs tracked in the local runs table.

Emissions are estimated from CPU·hours, GPU·hours, memory GB·hours and
walltime using the three-layer rate-card chain:

  Layer 0 — built-in SA defaults (Eskom 900 g CO₂e/kWh, CCF v3 coefficients)
  Layer 1 — ~/.abc/config.yaml  (abc config emissions set pue=1.27 ...)
  Layer 2 — per-invocation flags (--pue=, --grid-factor=)

Every report includes a "Rate card (effective)" footer showing which values
were used, their source, and the citation so the figure is defensible in
methods sections and grant reports.

Examples:
  abc report emissions
  abc report emissions --by=project --since=2026-01-01 --unit=t
  abc report emissions --by=namespace --output=csv > q1-2026-co2.csv
  abc report emissions --pue=1.27 --grid-factor=950

Override rate card for your facility:
  abc config emissions set pue=1.27 grid_factor_gco2_per_kwh=950`,
		Args: cobra.NoArgs, // flag-only; reject stray verbs like `emissions report`
		RunE: runEmissions,
	}

	cmd.Flags().String("by", "namespace",
		"Aggregation axis: namespace|project|investigation|user|pipeline")
	cmd.Flags().String("since", "",
		"Report window start (YYYY-MM-DD); default: Jan 1 of current year")
	cmd.Flags().String("until", "",
		"Report window end (YYYY-MM-DD); default: now")
	cmd.Flags().String("unit", "kg",
		"Display unit: kg (default) | t | g")
	cmd.Flags().String("output", "table",
		"Output format: table (default) | csv | json")
	cmd.Flags().String("rate-source", "full",
		"Rate card footer verbosity: full (default) | brief | none")
	cmd.Flags().Bool("include-incomplete", false,
		"Include running jobs (cost-so-far estimate; resource figures may be zero)")

	// Layer 2 flag overrides — mirror the config keys.
	cmd.Flags().Float64("pue", 0,
		"Override PUE for this invocation (Layer 2; advisory)")
	cmd.Flags().Float64("grid-factor", 0,
		"Override grid_factor_gco2_per_kwh for this invocation (advisory)")

	return cmd
}

func runEmissions(cmd *cobra.Command, _ []string) error {
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
	unit, err := parseEmissionsUnit(unitStr)
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
	layer2 := buildEmissionsLayer2(cmd)
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
		Mode:              acct.ModeEmissions,
		By:                by,
		Since:             since,
		Until:             until,
		ContextName:       contextName,
		IncludeIncomplete: includeIncomplete,
		Output:            output,
		RateSource:        rateSource,
		EmissionsUnit:     unit,
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
		until = until.Add(24*time.Hour - time.Second) // inclusive end of day
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

func parseEmissionsUnit(s string) (acct.EmissionsUnit, error) {
	switch s {
	case "kg", "":
		return acct.UnitKg, nil
	case "t":
		return acct.UnitT, nil
	case "g":
		return acct.UnitG, nil
	}
	return "", fmt.Errorf("--unit=%q not supported (allowed: kg, t, g)", s)
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

func buildEmissionsLayer2(cmd *cobra.Command) acct.FlagOverrides {
	out := acct.FlagOverrides{Emissions: map[string]string{}}
	if pue, _ := cmd.Flags().GetFloat64("pue"); pue > 0 {
		out.Emissions[acct.KeyPue] = fmt.Sprintf("%g", pue)
	}
	if gf, _ := cmd.Flags().GetFloat64("grid-factor"); gf > 0 {
		out.Emissions[acct.KeyGridFactor] = fmt.Sprintf("%g", gf)
	}
	return out
}

// keep os imported for future use.
var _ = os.Stderr
