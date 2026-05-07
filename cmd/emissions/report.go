package emissions

import (
	"fmt"
	"strings"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// addReportFlags wires the spec abc-emissions-accounting §D flags onto the
// parent emissions command for the new local-state report. Existing
// cloud-API behaviour (RunE = runEmissions) is preserved when --cloud is
// passed; otherwise the new local report runs.
func addReportFlags(cmd *cobra.Command) {
	cmd.Flags().String("by", "namespace", "group-by axis: namespace|project|investigation|user|pipeline")
	cmd.Flags().String("since", "", "report window start (YYYY-MM-DD); default 30 days ago")
	cmd.Flags().String("until", "", "report window end (YYYY-MM-DD); default now")
	cmd.Flags().String("currency", "", "(carried for parity; emissions reports do not use currency)")
	cmd.Flags().String("output", "table", "output format: table|csv|json")
	cmd.Flags().String("rate-source", "", "rate-card footer verbosity: full|brief|none")
	cmd.Flags().Bool("include-incomplete", false, "include runs with status='running'")
	cmd.Flags().String("unit", "kg", "emissions display unit: kg|t|g")
	cmd.Flags().Float64("pue", -1, "Layer 2 override: PUE (>= 1.0, <= 3.0)")
	cmd.Flags().Float64("grid-factor", -1, "Layer 2 override: grid factor g CO2e/kWh")
	cmd.Flags().Float64("cpu-w", -1, "Layer 2 override: watts per CPU")
	cmd.Flags().Float64("gpu-w", -1, "Layer 2 override: watts per GPU")
	cmd.Flags().Float64("memory-gb-w", -1, "Layer 2 override: watts per GB DRAM")
	cmd.Flags().Bool("all-contexts", false, "(Phase 2 — currently rejects with a clear error)")
	cmd.Flags().Bool("signed", false, "(Phase 2 — produce a server-signed, reproducible report; not yet implemented)")
	cmd.Flags().Bool("cloud", false, "Use the cloud GET /v1/emissions API instead of the local report")
}

// dispatchEmissions chooses between local SQLite report (default) and the
// existing cloud GET /v1/emissions API call (when --cloud is passed).
func dispatchEmissions(cmd *cobra.Command, args []string) error {
	if useCloud, _ := cmd.Flags().GetBool("cloud"); useCloud {
		return runEmissions(cmd, args)
	}
	return runLocalEmissionsReport(cmd, args)
}

func runLocalEmissionsReport(cmd *cobra.Command, _ []string) error {
	if all, _ := cmd.Flags().GetBool("all-contexts"); all {
		return fmt.Errorf("--all-contexts requires --currency=<code> in Phase 1; mixed-currency conversion is deferred (see specs/completed/abc-emissions-accounting.md \"Defers\")")
	}
	if signed, _ := cmd.Flags().GetBool("signed"); signed {
		return fmt.Errorf("--signed is not yet implemented; see brainstorms/emissions-accounting/2026-05-07-permissions-model.md (signing requires server-side rate cards which arrive with abc-grove)")
	}
	by, _ := cmd.Flags().GetString("by")
	output, _ := cmd.Flags().GetString("output")
	rateSource, _ := cmd.Flags().GetString("rate-source")
	includeIncomplete, _ := cmd.Flags().GetBool("include-incomplete")
	unit, _ := cmd.Flags().GetString("unit")

	since, until, err := resolveWindow(cmd)
	if err != nil {
		return err
	}
	contextName := state.ActiveContextName()

	layer1, err := acct.LoadLayer1(contextName)
	if err != nil {
		return fmt.Errorf("read config layer: %w", err)
	}
	flags := acct.FlagOverrides{Emissions: map[string]string{}, Accounting: map[string]string{}}
	type pair struct {
		key string
		v   float64
	}
	for _, p := range []pair{
		{acct.KeyPue, getF(cmd, "pue")},
		{acct.KeyGridFactor, getF(cmd, "grid-factor")},
		{acct.KeyCpuW, getF(cmd, "cpu-w")},
		{acct.KeyGpuW, getF(cmd, "gpu-w")},
		{acct.KeyMemoryGbW, getF(cmd, "memory-gb-w")},
	} {
		if p.v >= 0 {
			flags.Emissions[p.key] = fmt.Sprintf("%g", p.v)
		}
	}
	if v, _ := cmd.Flags().GetString("currency"); v != "" {
		flags.Accounting[acct.KeyCurrency] = strings.ToUpper(v)
	}

	card, err := acct.Resolve(acct.ZADefaults(), layer1, flags)
	if err != nil {
		return err
	}

	if by == "user" {
		fmt.Fprintln(cmd.ErrOrStderr(), "[abc] note: --by=user reports per-user totals; admin gating is Phase 2 (see spec deferral)")
	}

	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open state DB: %w", err)
	}
	opts := acct.ReportOptions{
		Mode:              acct.ModeEmissions,
		By:                acct.GroupBy(by),
		Since:             since,
		Until:             until,
		ContextName:       contextName,
		IncludeIncomplete: includeIncomplete,
		Output:            acct.OutputFormat(output),
		RateSource:        defaultRateSource(rateSource, output),
		EmissionsUnit:     acct.EmissionsUnit(unit),
	}
	rep, err := acct.Aggregate(cmd.Context(), db, opts, card)
	if err != nil {
		return err
	}
	return acct.Render(cmd.OutOrStdout(), rep, opts)
}

func defaultRateSource(user, output string) acct.RateSourceVerbosity {
	if user != "" {
		return acct.RateSourceVerbosity(user)
	}
	if output == "csv" {
		return acct.RateSourceNone
	}
	return acct.RateSourceFull
}

func resolveWindow(cmd *cobra.Command) (time.Time, time.Time, error) {
	const layout = "2006-01-02"
	now := time.Now()
	since := now.Add(-30 * 24 * time.Hour)
	until := now
	if v, _ := cmd.Flags().GetString("since"); v != "" {
		t, err := time.Parse(layout, v)
		if err != nil {
			return since, until, fmt.Errorf("--since: %w", err)
		}
		since = t
	}
	if v, _ := cmd.Flags().GetString("until"); v != "" {
		t, err := time.Parse(layout, v)
		if err != nil {
			return since, until, fmt.Errorf("--until: %w", err)
		}
		until = t.Add(24 * time.Hour)
	}
	return since, until, nil
}

func getF(cmd *cobra.Command, name string) float64 {
	v, _ := cmd.Flags().GetFloat64(name)
	return v
}
