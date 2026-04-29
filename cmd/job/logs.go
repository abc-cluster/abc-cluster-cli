package job

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "Stream or print logs for a Nomad batch job",
		Long: `Stream or print logs for a Nomad batch job.

By default logs are streamed from the Nomad alloc API (live only).
With --source=loki, logs are fetched from Loki using the task/alloc labels
written by Grafana Alloy — this works even after the allocation is GC'd and
supports --since, --grep, and --limit for historical queries.`,
		Args: cobra.ExactArgs(1),
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

func runLogs(cmd *cobra.Command, args []string) error {
	jobID := args[0]
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
		return fmt.Errorf("no allocations found for job %q", jobID)
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

// runLogsLoki queries Grafana Loki for historical logs of a job using the
// alloc labels written by Grafana Alloy (task=, alloc_id=, stream=).
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

	// Build LogQL selector.
	// Use job-level task filter when task is set; fall back to alloc prefix.
	var selectors []string
	if task != "" && task != "main" {
		selectors = append(selectors, fmt.Sprintf(`task="%s"`, task))
	}
	if allocPrefix != "" {
		selectors = append(selectors, fmt.Sprintf(`alloc_id=~"%s.*"`, allocPrefix))
	}
	if logType == "stderr" {
		selectors = append(selectors, `stream="stderr"`)
	} else {
		selectors = append(selectors, `stream="stdout"`)
	}

	// The job label written by Alloy is the Nomad job name under the "exported_job" label
	// on metrics. For logs, the filename path contains the job alloc dir.
	// We filter via task (most selective); for jobs with task="main" we fall back
	// to a filename filter.
	logql := "{" + strings.Join(selectors, ",") + "}"
	if task == "main" {
		// When using the default task name "main", the filter is too broad —
		// any job with a task named "main" would match. Add a grep for the job ID.
		grep = jobID + " " + grep
	}
	if grep != "" {
		logql += fmt.Sprintf(` |= %q`, strings.TrimSpace(grep))
	}

	lc := floor.NewLokiClient(lokiHTTP)
	entries, err := lc.QueryRange(cmd.Context(), logql, sinceStr, untilStr, limit)
	if err != nil {
		return fmt.Errorf("loki query: %w", err)
	}

	if len(entries) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"  No Loki logs found for job %q (query: %s)\n"+
				"  Labels indexed so far can be browsed at %s\n",
			jobID, logql, lokiHTTP)
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(cmd.ErrOrStderr(), "  %d log lines from Loki (query: %s)\n\n", len(entries), logql)
	for _, e := range entries {
		ts := e.Timestamp.Format("2006-01-02 15:04:05.000")
		task := e.Labels["task"]
		stream := e.Labels["stream"]
		prefix := fmt.Sprintf("[%s %s/%s] ", ts, task, stream)
		fmt.Fprintf(out, "%s%s\n", prefix, e.Line)
	}
	return nil
}
