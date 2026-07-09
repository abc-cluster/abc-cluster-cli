package job

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <job-id-or-nomad-ui-url>",
		Short: "Stream or print logs for a Nomad batch job",
		Long: `Stream or print logs for a Nomad batch job.

By default logs are streamed from the Nomad alloc API (live only).
With --source=loki, logs are fetched from Loki using the task/alloc labels
written by Grafana Alloy — this works even after the allocation is GC'd and
supports --since, --grep, and --limit for historical queries.

The positional accepts either a bare job ID or a full Nomad Web UI URL.
When a URL is given, --namespace / --alloc / --task are seeded from the
URL unless explicitly overridden by the corresponding flag. The hostname
in the URL is ignored (it varies by tier — nomad.seedling.<…>, nomad.grove.<…>,
nomad.cloud.<…> — but the path-and-query schema is identical), so URLs
pasted from any tier's Nomad UI work the same way:

    abc job logs https://nomad.seedling.abc-cluster.cloud/ui/jobs/<job>@<ns>/allocations?activeTask=<alloc-uuid>-<task>

A job id is optional when --alloc <id> is given: logs are then read directly
from that allocation (useful when the job record is gone but the alloc is
still live on its client).`,
		Args: cobra.MaximumNArgs(1),
		RunE: runLogs,
	}
	cmd.Flags().BoolP("follow", "f", false, "Stream logs in real time (nomad source only)")
	cmd.Flags().String("alloc", "", "Filter to a specific allocation ID prefix")
	cmd.Flags().String("task", "main", "Task name within the allocation")
	cmd.Flags().String("type", "stdout", "Log type: stdout or stderr")
	cmd.Flags().String("namespace", "", "Nomad namespace")
	cmd.Flags().String("since", "", "Show logs since this time: RFC3339 timestamp or duration (e.g. 2h, 30m)")
	cmd.Flags().String("until", "", "Show logs until this RFC3339 timestamp (loki source only)")
	cmd.Flags().String("source", "nomad", "Log source: nomad (live) or loki (historical, requires capabilities)")
	cmd.Flags().String("grep", "", "Filter log lines by substring (loki source only)")
	cmd.Flags().Int("limit", 500, "Maximum number of log lines (loki source only)")
	cmd.Flags().String("output", "", "Write stdout logs to file (nomad source only)")
	cmd.Flags().String("error", "", "Write stderr logs to file (nomad source only)")
	return cmd
}

// resolveAllocTask picks the task to stream logs from. It honours an explicit
// --task (erroring with the available tasks if it's wrong), and auto-resolves
// the default "main" against the allocation's real tasks: a single-task alloc
// uses that task; a Nextflow head job resolves to "nextflow"/"nf-task".
func resolveAllocTask(cmd *cobra.Command, target *NomadAllocStub, requested string) (string, error) {
	if target == nil || len(target.TaskStates) == 0 {
		return requested, nil // nothing to resolve against; let StreamLogs try
	}
	if _, ok := target.TaskStates[requested]; ok {
		return requested, nil
	}
	names := make([]string, 0, len(target.TaskStates))
	for n := range target.TaskStates {
		names = append(names, n)
	}
	sort.Strings(names)
	id := target.ID
	if len(id) > 8 {
		id = id[:8]
	}
	// An explicit --task that doesn't exist → clear error listing the options.
	if cmd.Flags().Changed("task") {
		return "", fmt.Errorf("task %q not found in allocation %s; available tasks: %s",
			requested, id, strings.Join(names, ", "))
	}
	// Default "main" not present → auto-resolve.
	if len(names) == 1 {
		return names[0], nil
	}
	for _, pref := range []string{"nextflow", "nf-task", "main"} {
		if _, ok := target.TaskStates[pref]; ok {
			return pref, nil
		}
	}
	return "", fmt.Errorf("allocation %s has multiple tasks; pick one with --task: %s",
		id, strings.Join(names, ", "))
}

func runLogs(cmd *cobra.Command, args []string) error {
	// Allow `abc job logs --alloc <id>` with no job positional. Previously
	// Args=ExactArgs(1) rejected it as "accepts 1 arg(s), received 0" (B2);
	// now an alloc-only invocation streams straight from that allocation.
	if len(args) == 0 {
		allocOnly, _ := cmd.Flags().GetString("alloc")
		if strings.TrimSpace(allocOnly) == "" {
			return fmt.Errorf("provide a job id, or --alloc <id> to read a specific allocation")
		}
		return runLogsByAlloc(cmd, strings.TrimSpace(allocOnly))
	}

	// Accept either a bare job ID or a Nomad Web UI URL. When a URL is
	// given, --namespace / --alloc / --task are seeded from it unless the
	// user passed those flags explicitly. See resolveJobArg in url.go.
	jobID, err := resolveJobArg(cmd, args[0])
	if err != nil {
		return err
	}

	source, _ := cmd.Flags().GetString("source")

	if source == "loki" {
		return runLogsLoki(cmd, jobID)
	}

	follow, _ := cmd.Flags().GetBool("follow")
	allocPrefix, _ := cmd.Flags().GetString("alloc")
	task, _ := cmd.Flags().GetString("task")
	logType, _ := cmd.Flags().GetString("type")
	ns := namespaceFromCmd(cmd)
	sinceStr, _ := cmd.Flags().GetString("since")

	if logType != "stdout" && logType != "stderr" {
		return fmt.Errorf("--type must be stdout or stderr, got %q", logType)
	}

	var sinceOffset int64
	if sinceStr != "" {
		t, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			return fmt.Errorf("--since must be RFC3339, got %q: %w", sinceStr, err)
		}
		sinceOffset = t.UnixNano()
	}
	_ = sinceOffset // used by StreamLogs offset in future; always start from 0 for now

	nc := nomadClientFromCmd(cmd)

	// Find the target allocation.
	allocs, err := nc.GetJobAllocs(cmd.Context(), jobID, ns, false)
	if err != nil {
		return fmt.Errorf("getting allocations for job %q: %w", jobID, err)
	}

	if len(allocs) == 0 {
		// Nomad has no allocs for this job — the alloc dirs may have been
		// GC'd from the client, OR the user typo'd. Auto-fall through to
		// VictoriaLogs (historical archive, 90d retention) if it's
		// configured for this context; that still works because the
		// Nomad SERVER retains the job's metadata for `job_gc_threshold`
		// (set to 8760h on seedling). When the loki path can't reach
		// VictoriaLogs either, both errors are surfaced.
		cfg, cfgErr := config.Load()
		if cfgErr == nil && cfg != nil {
			ctxCfg := cfg.ActiveCtx()
			if v, ok := config.GetAdminFloorField(&ctxCfg.Admin.Services, "loki", "http"); ok && v != "" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"  No live allocations in Nomad for job %q — falling back to VictoriaLogs (historical archive).\n",
					jobID)
				return runLogsLoki(cmd, jobID)
			}
		}
		return fmt.Errorf("no allocations found for job %q (and no loki/victorialogs endpoint configured for fallback — set admin.services.loki.http or run `abc cluster capabilities sync`)", jobID)
	}

	// Filter by prefix if provided.
	var target *NomadAllocStub
	for i := range allocs {
		a := &allocs[i]
		if allocPrefix != "" && !strings.HasPrefix(a.ID, allocPrefix) {
			continue
		}
		// Prefer running allocations; fall back to the most recent.
		if target == nil || a.ClientStatus == "running" {
			target = a
		}
	}
	if target == nil {
		return fmt.Errorf("no allocation matching %q found for job %q", allocPrefix, jobID)
	}

	// Resolve the task name. The default --task "main" is the abc job-run
	// convention but does not exist on Nextflow head jobs (task "nextflow") or
	// many other jobs; auto-resolve against the allocation's actual tasks so
	// `abc job logs <job>` just works without the caller knowing the task name.
	task, err = resolveAllocTask(cmd, target, task)
	if err != nil {
		return err
	}

	origin := "start"
	if !follow && sinceStr == "" {
		// Non-follow: read from end to show recent output.
		origin = "end"
	}

	// Only demote follow for terminal allocs. Pending/starting must keep
	// follow=true or the log API returns a snapshot and EOF immediately.
	if follow && utils.AllocClientTerminalStatus(target.ClientStatus) {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Allocation %s is %s; showing completed logs and exiting.\n\n", target.ID[:8], target.ClientStatus)
		follow = false
		origin = "start"
	}

	out := cmd.OutOrStdout()
	if follow {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Streaming logs for alloc %s (task: %s)...\n\n",
			target.ID[:8], task)
	}

	outputPath, _ := cmd.Flags().GetString("output")
	errorPath, _ := cmd.Flags().GetString("error")

	if outputPath != "" || errorPath != "" {
		if outputPath != "" {
			if logType != "stdout" {
				return fmt.Errorf("--output requires --type stdout")
			}
			f, err := os.Create(outputPath)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := nc.StreamLogs(cmd.Context(), target.ID, task, "stdout", origin, 0, follow, f); err != nil {
				return err
			}
		}
		if errorPath != "" {
			if logType != "stderr" {
				return fmt.Errorf("--error requires --type stderr")
			}
			f, err := os.Create(errorPath)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := nc.StreamLogs(cmd.Context(), target.ID, task, "stderr", origin, 0, follow, f); err != nil {
				return err
			}
		}
		return nil
	}

	_, err = nc.StreamLogs(cmd.Context(), target.ID, task, logType, origin, 0, follow, out)
	return err
}

// runLogsByAlloc streams logs for a single allocation supplied via --alloc with
// no job id. The Nomad fs log API is client-local, so this works whenever the
// alloc dir still survives on its node — even if the job record is gone or the
// caller doesn't know the job id (B2).
func runLogsByAlloc(cmd *cobra.Command, allocID string) error {
	logType, _ := cmd.Flags().GetString("type")
	if logType != "stdout" && logType != "stderr" {
		return fmt.Errorf("--type must be stdout or stderr, got %q", logType)
	}
	task, _ := cmd.Flags().GetString("task")
	follow, _ := cmd.Flags().GetBool("follow")
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)

	// Resolve the real task name against the alloc's tasks when we can read it;
	// otherwise fall through with the requested/default task and let StreamLogs
	// report the valid task names.
	if alloc, err := nc.GetAllocation(cmd.Context(), allocID, ns); err == nil && alloc != nil {
		stub := &NomadAllocStub{ID: alloc.ID, ClientStatus: alloc.ClientStatus, TaskStates: alloc.TaskStates}
		resolved, terr := resolveAllocTask(cmd, stub, task)
		if terr != nil {
			return terr
		}
		task = resolved
		if follow && utils.AllocClientTerminalStatus(alloc.ClientStatus) {
			sid := allocID
			if len(sid) > 8 {
				sid = sid[:8]
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "  Allocation %s is %s; showing completed logs and exiting.\n\n", sid, alloc.ClientStatus)
			follow = false
		}
	}

	origin := "end"
	if follow {
		origin = "start"
	}
	_, err := nc.StreamLogs(cmd.Context(), allocID, task, logType, origin, 0, follow, cmd.OutOrStdout())
	return err
}

// runLogsLoki queries the cluster's log archive (VictoriaLogs today; Loki-API-
// compatible) for historical logs of a job. The Alloy ship-side labels are
// `{alloc_id, task, stream}` so we need to know the job's alloc IDs to filter
// precisely — otherwise a query like `{task="nf-task"}` would match every
// nf-nomad task across every job in history.
//
// Resolution order for the alloc list:
//  1. Nomad's /v1/job/<id>/allocations (server-side, retained for
//     job_gc_threshold = 1 year on seedling). This is the source of truth
//     for jobs whose server records are still live.
//  2. If the caller passed --alloc <prefix> explicitly, use it as a regex
//     prefix and skip the Nomad lookup (useful when the job itself has been
//     server-side GC'd but the user knows an alloc ID).
//  3. If neither yields any allocs, return a clear error.
//
// Future (see brainstorms/abc-seedling-prod/2026-06-03-persistent-job-logs.md
// — "Stage 2"): once Alloy is enriched with `job_id` / `namespace` labels via
// discovery.nomad, this function will be able to skip the Nomad lookup
// entirely and query VictoriaLogs by job_id directly. The current alloc-list
// path stays as a fallback for older logs that pre-date the label addition.
func runLogsLoki(cmd *cobra.Command, jobID string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()

	lokiHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "loki", "http")
	if !ok || lokiHTTP == "" {
		return fmt.Errorf(
			"loki URL not configured for context %q\n"+
				"  Run: abc cluster capabilities sync\n"+
				"  Or:  abc config set admin.services.loki.http http://<ip>:3100",
			cfg.ActiveContext,
		)
	}

	task, _ := cmd.Flags().GetString("task")
	logType, _ := cmd.Flags().GetString("type")
	sinceStr, _ := cmd.Flags().GetString("since")
	untilStr, _ := cmd.Flags().GetString("until")
	grep, _ := cmd.Flags().GetString("grep")
	limit, _ := cmd.Flags().GetInt("limit")
	allocPrefix, _ := cmd.Flags().GetString("alloc")
	ns := namespaceFromCmd(cmd)

	// ── Discover alloc IDs for this job ─────────────────────────────────
	// Look up the alloc list from Nomad first; if --alloc was explicitly
	// passed, fall back to a regex-prefix filter (no Nomad lookup).
	var allocIDs []string
	if allocPrefix == "" {
		nc := nomadClientFromCmd(cmd)
		allocs, err := nc.GetJobAllocs(cmd.Context(), jobID, ns, false)
		if err != nil {
			// Don't abort: Nomad may have GC'd the job entirely (post
			// 1-year retention). Surface the error as a hint and let the
			// query proceed without an alloc filter — once Stage 2 lands,
			// the job_id label will carry us; until then we surface 0
			// results cleanly.
			fmt.Fprintf(cmd.ErrOrStderr(),
				"  Note: Nomad lookup for job %q failed (%v) — querying VictoriaLogs without alloc_id filter.\n",
				jobID, err)
		}
		for i := range allocs {
			allocIDs = append(allocIDs, allocs[i].ID)
		}
	}

	// ── Build the LogQL selector ────────────────────────────────────────
	var selectors []string
	switch {
	case len(allocIDs) > 0:
		// Precise: one alloc_id regex spanning every alloc this job had.
		// Use anchors so a partial prefix can't drag in unrelated allocs.
		selectors = append(selectors,
			fmt.Sprintf(`alloc_id=~"^(%s)$"`, strings.Join(allocIDs, "|")))
	case allocPrefix != "":
		selectors = append(selectors, fmt.Sprintf(`alloc_id=~"^%s.*"`, allocPrefix))
	}
	if task != "" && task != "main" {
		selectors = append(selectors, fmt.Sprintf(`task="%s"`, task))
	}
	if logType == "stderr" {
		selectors = append(selectors, `stream="stderr"`)
	} else {
		selectors = append(selectors, `stream="stdout"`)
	}

	// Defensive: refuse a query that would match every alloc on the cluster.
	if len(allocIDs) == 0 && allocPrefix == "" {
		return fmt.Errorf(
			"could not resolve any allocations for job %q (namespace=%q).\n"+
				"  • If the job is older than Nomad's 1-year retention, pass --alloc <id-prefix>.\n"+
				"  • Once `job_id` labels are wired up via Alloy (Stage 2 — see brainstorms/abc-seedling-prod/2026-06-03-persistent-job-logs.md), this lookup becomes unnecessary.",
			jobID, ns)
	}

	// Default `task=main` is the abc job-run convention and far too broad
	// for a label match alone — narrow it with a substring grep on the
	// job ID, same as before.
	if task == "main" {
		grep = jobID + " " + grep
	}

	// Dispatch to the right backend. abc-nodes deployments use Grafana
	// Loki at `:3100` (LogQL); abc-seedling uses VictoriaLogs at `:9428`
	// (LogSQL — Loki INSERT is supported, but query is LogSQL only).
	var entries []floor.LokiEntry
	var logqlForMsg string
	backend := floor.DetectLogsBackend(lokiHTTP)
	switch backend {
	case floor.BackendVictoriaLogs:
		// LogSQL form: `_stream:{label=…} <extra> _time:[…]`. The selectors
		// we already built use the same `key="value"` / `key=~"regex"` shape
		// LogSQL accepts inside `_stream:{…}` — so we can pass them through
		// after stripping the trailing alloc/task/stream → already there.
		streamSel := "{" + strings.Join(selectors, ",") + "}"
		var extra string
		if grep != "" {
			// LogSQL: free-text words filter — un-anchored substring.
			extra = strconv.Quote(strings.TrimSpace(grep))
		}
		logqlForMsg = "_stream:" + streamSel
		if extra != "" {
			logqlForMsg += " " + extra
		}
		vc := floor.NewVictoriaLogsClient(lokiHTTP)
		entries, err = vc.QueryStream(cmd.Context(), streamSel, extra, sinceStr, untilStr, limit)
		if err != nil {
			return fmt.Errorf("victorialogs query: %w", err)
		}
	default:
		// LogQL form (Grafana Loki).
		logql := "{" + strings.Join(selectors, ",") + "}"
		if grep != "" {
			logql += fmt.Sprintf(` |= %q`, strings.TrimSpace(grep))
		}
		logqlForMsg = logql
		lc := floor.NewLokiClient(lokiHTTP)
		entries, err = lc.QueryRange(cmd.Context(), logql, sinceStr, untilStr, limit)
		if err != nil {
			return fmt.Errorf("loki query: %w", err)
		}
	}
	logql := logqlForMsg

	if len(entries) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"  No archived logs found for job %q.\n"+
				"  • LogQL: %s\n"+
				"  • Endpoint: %s\n"+
				"  • Hint: VictoriaLogs retention is 90d by default — older logs may have aged out.\n",
			jobID, logql, lokiHTTP)
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(cmd.ErrOrStderr(),
		"  %d log lines from VictoriaLogs archive (job %q, %d alloc(s))\n  Query: %s\n\n",
		len(entries), jobID, len(allocIDs), logql)
	for _, e := range entries {
		ts := e.Timestamp.Format("2006-01-02 15:04:05.000")
		t := e.Labels["task"]
		stream := e.Labels["stream"]
		prefix := fmt.Sprintf("[%s %s/%s] ", ts, t, stream)
		fmt.Fprintf(out, "%s%s\n", prefix, e.Line)
	}
	return nil
}
