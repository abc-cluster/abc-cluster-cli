// render_runs.go — text renderer for `abc report runs`.
//
// Jobs-first table with cost + CO₂e columns. Pipeline rows render "—"
// in cost / CO₂e cells because the head-orchestrator-only resource
// numbers we have today would mislead by 10-100× if multiplied through
// the rate card. The footnote explains the pending state honestly.

package report

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// RunsTextOptions tunes the text renderer.
type RunsTextOptions struct {
	// Currency symbol — derived the same way the headline report does
	// it. "R" for ZAR, otherwise the alpha code.
	CurrencySymbol string

	// Total is the unfiltered count (the COUNT(*) twin of the rows query)
	// used to render the "Showing N of M" footer so a researcher with
	// thousands of rows knows when to widen --since or --limit.
	Total int

	// Window is rendered in the header so the operator sees what
	// period the table reflects.
	Since time.Time
	Until time.Time

	// Verb is the active filter string (or "" for all). Echoed in the
	// header so `abc report runs --verb=job` produces a self-describing
	// table.
	Verb string

	// Full switches the renderer to a per-row vertical block layout
	// surfacing the forensic fields (run_id, nomad_job_id, exit_code,
	// exit_reason, namespace, project / investigation attachments,
	// workdir_root, params_json) that the default columnar view hides.
	// Useful for tracing a specific row to its Nomad job (`abc job
	// show <nomad_job_id>`) or auditing what flags were captured at
	// submit time. Off by default to keep the default surface scannable.
	Full bool
}

// RenderRunsText writes the jobs-first run table to w. The width of
// the workload column is computed dynamically (clipped at 32 chars)
// so a typical screen renders without horizontal scroll.
//
// When opts.Full is set the renderer switches to a per-row vertical
// block layout — see renderRunsTextFull — which surfaces the forensic
// fields the columnar view deliberately hides.
func RenderRunsText(w io.Writer, rows []RunRow, opts RunsTextOptions) error {
	currSym := opts.CurrencySymbol
	if currSym == "" {
		currSym = "R"
	}

	// Header
	header := "Runs"
	if opts.Verb != "" {
		header = "Runs (verb=" + opts.Verb + ")"
	}
	if !opts.Since.IsZero() || !opts.Until.IsZero() {
		header += " · " + formatWindow(opts.Since, opts.Until)
	}
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, strings.Repeat("─", 76))

	if len(rows) == 0 {
		fmt.Fprintln(w, "(no runs in this window)")
		fmt.Fprintln(w)
		return nil
	}

	if opts.Full {
		return renderRunsTextFull(w, rows, currSym, opts)
	}

	// Compute dynamic workload column width (clip at 32).
	workloadW := len("WORKLOAD")
	for _, r := range rows {
		if n := len(r.WorkloadRef); n > workloadW {
			workloadW = n
		}
	}
	if workloadW > 32 {
		workloadW = 32
	}

	// Column headers — chose fixed widths for the numerics so columns
	// line up across rows of widely varying magnitude.
	fmt.Fprintf(w, "%-16s  %-8s  %-*s  %7s  %9s  %10s  %s\n",
		"TIME (UTC)", "VERB", workloadW, "WORKLOAD",
		"CPU·hr", "COST("+currSym+")", "CO₂e (kg)", "STATUS")

	pipelinePendingShown := false
	for _, r := range rows {
		costCell := fmt.Sprintf("%.2f", r.CostZAR)
		emCell := fmt.Sprintf("%.2f", r.EmissionsKgCO2e)
		if r.CostPending {
			costCell = "—"
			emCell = "—"
			pipelinePendingShown = true
		}

		fmt.Fprintf(w, "%-16s  %-8s  %-*s  %7.2f  %9s  %10s  %s\n",
			r.SubmittedAt.Format("2006-01-02 15:04"),
			r.Verb,
			workloadW, truncate(r.WorkloadRef, workloadW),
			r.CPUHours,
			costCell, emCell,
			compactStatus(r.Status),
		)
	}

	fmt.Fprintln(w)
	if pipelinePendingShown {
		fmt.Fprintln(w, "(—) Pipeline cost/emissions are pending: the head-orchestrator's")
		fmt.Fprintln(w, "    resources alone undercount the real pipeline by 10-100×, so the")
		fmt.Fprintln(w, "    numbers are intentionally blank until the per-task cache aggregator")
		fmt.Fprintln(w, "    ships (Khan v1). Job rows are computed directly from Nomad alloc")
		fmt.Fprintln(w, "    resources and are honest.")
		fmt.Fprintln(w)
	}

	if opts.Total > len(rows) {
		fmt.Fprintf(w, "Showing %d of %d runs. Use --since / --until / --limit / --verb to filter.\n",
			len(rows), opts.Total)
	} else {
		fmt.Fprintf(w, "%d run(s).\n", len(rows))
	}
	return nil
}

// renderRunsTextFull emits one vertical block per row with every
// forensic field laid out as `Label  Value`. Field order is fixed so
// `grep` over the output finds what you'd expect ("Nomad job" always
// near the top of each block). Empty / NULL fields render as "—" so
// the absence is visible (forensics: knowing a row HAS no investigation
// is just as informative as knowing what investigation it has).
func renderRunsTextFull(w io.Writer, rows []RunRow, currSym string, opts RunsTextOptions) error {
	pipelinePendingShown := false
	for i, r := range rows {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, strings.Repeat("━", 76))
		fmt.Fprintf(w, "%s · %s · %s\n",
			r.RunID, r.Verb, r.SubmittedAt.Format("2006-01-02 15:04 UTC"))
		fmt.Fprintln(w, strings.Repeat("─", 76))

		// Identity + workload block
		printField(w, "Workload", r.WorkloadRef)
		if r.WorkloadVersion != "" {
			printField(w, "Version", r.WorkloadVersion)
		}
		printField(w, "Status", compactStatus(r.Status))
		if r.CompletedAt != nil {
			printField(w, "Completed at", r.CompletedAt.Format("2006-01-02 15:04 UTC"))
		} else {
			printField(w, "Completed at", "—  (still running OR watcher never reconciled)")
		}

		// Cost-bearing block
		fmt.Fprintln(w)
		printField(w, "CPU·hr", fmt.Sprintf("%.4f", r.CPUHours))
		printField(w, "Mem·GB·hr", fmt.Sprintf("%.4f", r.MemGBHours))
		printField(w, "Walltime", formatSeconds(r.WalltimeSec))
		if r.CostPending {
			printField(w, "Cost", "—  (pipeline cost pending; see footnote)")
			printField(w, "CO₂e (kg)", "—  (pipeline cost pending; see footnote)")
			pipelinePendingShown = true
		} else {
			printField(w, "Cost", fmt.Sprintf("%s %.4f", currSym, r.CostZAR))
			printField(w, "CO₂e (kg)", fmt.Sprintf("%.4f", r.EmissionsKgCO2e))
		}

		// Forensic / tracing block
		fmt.Fprintln(w)
		printField(w, "Nomad job", dashIfEmpty(r.NomadJobID))
		printField(w, "Namespace", dashIfEmpty(r.Namespace))
		if r.ExitCode != 0 || r.ExitReason != "" {
			printField(w, "Exit code", fmt.Sprintf("%d", r.ExitCode))
			printField(w, "Exit reason", dashIfEmpty(r.ExitReason))
		}

		// Attachment / pipeline-specific block
		printField(w, "Project", dashIfEmpty(r.ProjectID))
		printField(w, "Investigation", dashIfEmpty(r.InvestigationID))
		if r.FreezeID != "" {
			printField(w, "Freeze", r.FreezeID)
		}
		if r.WorkdirRoot != "" {
			printField(w, "Workdir root", r.WorkdirRoot)
		}
		if r.ParamsJSON != "" {
			// Params can be a long JSON blob — show it on its own line
			// (no Label-aligned indent) so it's grep-friendly.
			fmt.Fprintln(w)
			fmt.Fprintln(w, "Params:")
			fmt.Fprintln(w, "  "+r.ParamsJSON)
		}
	}

	fmt.Fprintln(w)
	if pipelinePendingShown {
		fmt.Fprintln(w, "(—) Pipeline cost/emissions are pending: the head-orchestrator's")
		fmt.Fprintln(w, "    resources alone undercount the real pipeline by 10-100×, so the")
		fmt.Fprintln(w, "    numbers are intentionally blank until the per-task cache aggregator")
		fmt.Fprintln(w, "    ships (Khan v1). Job rows are computed directly from Nomad alloc")
		fmt.Fprintln(w, "    resources and are honest.")
		fmt.Fprintln(w)
	}
	if opts.Total > len(rows) {
		fmt.Fprintf(w, "Showing %d of %d runs. Use --since / --until / --limit / --verb to filter.\n",
			len(rows), opts.Total)
	} else {
		fmt.Fprintf(w, "%d run(s).\n", len(rows))
	}
	return nil
}

// printField writes one `Label  Value` line aligned to a fixed column.
// 14 chars gives enough room for "Investigation" without padding most
// other labels into awkwardness.
func printField(w io.Writer, label, value string) {
	fmt.Fprintf(w, "  %-14s  %s\n", label, value)
}

// dashIfEmpty returns "—" for empty strings so renderRunsTextFull never
// emits a blank value (forensic principle: explicit absence vs missing
// field is information).
func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// formatSeconds renders walltime compactly: "3m 5s" / "2h 14m 8s" /
// "—" when unknown.
func formatSeconds(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm %ds", sec/60, sec%60)
	}
	h := sec / 3600
	rem := sec % 3600
	return fmt.Sprintf("%dh %dm %ds", h, rem/60, rem%60)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// compactStatus shortens a long status/exit_reason combo into a one-
// or two-word marker that fits the trailing column. "completed" /
// "failed" / "running" / "lost" are the common shapes from runner.Watch.
func compactStatus(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// formatWindow renders the report window human-readably for the header.
// Zero values become "open" so partial windows make sense
// (`--since=7d` with no until shows "since 2026-05-20").
func formatWindow(since, until time.Time) string {
	switch {
	case since.IsZero() && until.IsZero():
		return ""
	case until.IsZero():
		return "since " + since.UTC().Format("2006-01-02")
	case since.IsZero():
		return "until " + until.UTC().Format("2006-01-02")
	default:
		return since.UTC().Format("2006-01-02") + " → " + until.UTC().Format("2006-01-02")
	}
}
