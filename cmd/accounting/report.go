package accounting

import (
	"fmt"
	"strings"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// addReportFlags wires the spec abc-emissions-accounting §D flags onto the
// parent accounting command. The new local-state report runs as the
// command's default RunE when no subcommand is given.
func addReportFlags(cmd *cobra.Command) {
	cmd.Flags().String("by", "namespace", "group-by axis: namespace|project|investigation|user|pipeline")
	cmd.Flags().String("since", "", "report window start (YYYY-MM-DD); default 30 days ago")
	cmd.Flags().String("until", "", "report window end (YYYY-MM-DD); default now")
	cmd.Flags().String("currency", "", "override the rate-card currency (ISO-4217 alpha)")
	cmd.Flags().String("output", "table", "output format: table|csv|json")
	cmd.Flags().String("rate-source", "", "rate-card footer verbosity: full|brief|none (default: full for table, none for csv)")
	cmd.Flags().Bool("include-incomplete", false, "include runs with status='running'")
	cmd.Flags().Float64("rate-cpu-hour", -1, "Layer 2 override: ZAR (or --currency) per CPU·hour")
	cmd.Flags().Float64("rate-gpu-hour", -1, "Layer 2 override: cost per GPU·hour")
	cmd.Flags().Float64("rate-memory-gb-hour", -1, "Layer 2 override: cost per GB·hour memory")
	cmd.Flags().Bool("all-contexts", false, "(Phase 2 — currently rejects with a clear error)")
	cmd.Flags().Bool("signed", false, "(Phase 2 — produce a server-signed, reproducible report; not yet implemented)")
	cmd.RunE = runLocalAccountingReport
}

func runLocalAccountingReport(cmd *cobra.Command, _ []string) error {
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

	since, until, err := resolveWindow(cmd)
	if err != nil {
		return err
	}

	contextName := state.ActiveContextName()

	// Layer 1 — read config blocks.
	layer1, err := acct.LoadLayer1(contextName)
	if err != nil {
		return fmt.Errorf("read config layer: %w", err)
	}
	// Layer 2 — flag overrides.
	flags := acct.FlagOverrides{
		Accounting: map[string]string{},
	}
	if v, _ := cmd.Flags().GetFloat64("rate-cpu-hour"); v >= 0 {
		flags.Accounting[acct.KeyCostCpuHour] = fmtFloat(v)
	}
	if v, _ := cmd.Flags().GetFloat64("rate-gpu-hour"); v >= 0 {
		flags.Accounting[acct.KeyCostGpuHour] = fmtFloat(v)
	}
	if v, _ := cmd.Flags().GetFloat64("rate-memory-gb-hour"); v >= 0 {
		flags.Accounting[acct.KeyCostMemoryGbHour] = fmtFloat(v)
	}
	if v, _ := cmd.Flags().GetString("currency"); v != "" {
		flags.Accounting[acct.KeyCurrency] = strings.ToUpper(v)
	}

	card, err := acct.Resolve(acct.ZADefaults(), layer1, flags)
	if err != nil {
		return err
	}

	// --by=user is admin-gated; we ship a permissive Phase 1 implementation
	// (see spec deferral). Document via warning.
	if by == "user" {
		fmt.Fprintln(cmd.ErrOrStderr(), "[abc] note: --by=user reports per-user totals; admin gating is Phase 2 (see spec deferral)")
	}

	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open state DB: %w", err)
	}
	opts := acct.ReportOptions{
		Mode:              acct.ModeAccounting,
		By:                acct.GroupBy(by),
		Since:             since,
		Until:             until,
		ContextName:       contextName,
		IncludeIncomplete: includeIncomplete,
		Output:            acct.OutputFormat(output),
		RateSource:        defaultRateSource(rateSource, output),
	}
	rep, err := acct.Aggregate(cmd.Context(), db, opts, card)
	if err != nil {
		return err
	}
	return acct.Render(cmd.OutOrStdout(), rep, opts)
}

// defaultRateSource returns the user's choice if set; otherwise the
// per-output-format default ("none" for csv, "full" for table, "full"
// for json — JSON always carries the rate_card key).
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
		until = t.Add(24 * time.Hour) // inclusive end-of-day
	}
	return since, until, nil
}

func fmtFloat(f float64) string {
	return fmt.Sprintf("%g", f)
}
