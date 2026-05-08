package accounting

import (
	"fmt"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/capability"
	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// reportCapabilities declares what `abc accounting report` needs.
//
// AnyOf: prefer abc-accounting-svc (Kayastha); fall back to local-state
// (the local SQLite). Today only local-state is deployed at every tier;
// abc-accounting-svc lands with abc-grove + Kayastha v0. Either backend
// is sufficient for the per-run aggregation surface.
//
// OptionalFor:
//   - --signed requires abc-fleet-svc (Veld; Layer-1 rate-card source).
//     Without Veld, signed reports cannot be produced — fail-fast.
//   - --all-contexts requires the Kayastha service with the
//     federation-aggregate feature; cross-context aggregation needs
//     server-side joining over multiple clusters' event streams.
var reportCapabilities = capability.Required{
	AnyOf: []capability.Need{
		{Service: "abc-accounting-svc", MinVersion: "0.4.0",
			Features: []string{"per-run-aggregate"}},
		{Service: "local-state", MinVersion: "0001_initial",
			Features: []string{"runs", "projects", "investigations"}},
	},
	OptionalFor: map[string]capability.Need{
		"signed": {Service: "abc-fleet-svc", MinVersion: "0.5.0"},
		"all-contexts": {Service: "abc-accounting-svc", MinVersion: "0.6.0",
			Features: []string{"federation-aggregate"}},
	},
}

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
	// ── Capability gate (Stage A wiring) ──────────────────────────────────────
	// Resolve the verb's capability needs against the active context's
	// Capabilities block. Without a populated Services map (the common
	// case today since Khan isn't deployed), local-state pseudo-service
	// satisfies the AnyOf and the verb runs locally. --signed and
	// --all-contexts surface clean capability errors when their backing
	// services aren't deployed.
	caps := loadActiveContextCapabilities()
	flagsSet := map[string]bool{
		"signed":       boolFlag(cmd, "signed"),
		"all-contexts": boolFlag(cmd, "all-contexts"),
	}
	decision := capability.Require(reportCapabilities, caps, flagsSet)
	if decision.Failed() {
		return decision.AsError()
	}
	if decision.Banner != "" && !quietMode(cmd) {
		fmt.Fprintln(cmd.ErrOrStderr(), decision.Banner)
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

// loadActiveContextCapabilities returns the Capabilities block stored
// for the active context. Returns nil if config can't be loaded or no
// active context is set — capability.Require treats nil as "Services
// map empty; local-state available via fallback".
func loadActiveContextCapabilities() *config.Capabilities {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	ctxName := cfg.ResolveContextName(cfg.ActiveContext)
	if ctxName == "" {
		return nil
	}
	ctx := cfg.Contexts[ctxName]
	return ctx.Capabilities
}

// boolFlag is a helper that returns false if the flag isn't defined or
// can't be read. Saves four lines per call site at the verb level.
func boolFlag(cmd *cobra.Command, name string) bool {
	if cmd == nil {
		return false
	}
	v, _ := cmd.Flags().GetBool(name)
	return v
}

// quietMode returns true when --quiet/-q is set or ABC_QUIET=1 in env.
// Used to suppress capability degradation banners for scripted use.
func quietMode(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("quiet"); v {
		return true
	}
	return false
}
