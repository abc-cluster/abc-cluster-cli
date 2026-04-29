package job

import (
	"fmt"
	"os"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

// Exit codes for abc job status:
//   0 = job complete/succeeded
//   1 = job dead/failed
//   2 = job still running or pending
//   3 = error reaching Nomad

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <job-id>",
		Short: "Print a compact status summary for a Nomad batch job",
		Long: `Print a one-line status summary and exit with a code reflecting the job outcome.

Exit codes:
  0  Job complete with no failures
  1  Job dead or failed
  2  Job still running or pending
  3  Error reaching Nomad or job not found`,
		Args:              cobra.ExactArgs(1),
		RunE:              runStatus,
		SilenceUsage:      true,
	}
	cmd.Flags().String("namespace", "", "Nomad namespace")
	cmd.Flags().Bool("metrics", false, "Show live CPU/memory metrics per alloc from Prometheus (requires capabilities)")
	return cmd
}

func runStatus(cmd *cobra.Command, args []string) error {
	jobID := args[0]
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)

	job, err := nc.GetJob(cmd.Context(), jobID, ns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		os.Exit(3)
	}

	allocs, _ := nc.GetJobAllocs(cmd.Context(), jobID, ns, false)

	running, succeeded, failed := 0, 0, 0
	for _, a := range allocs {
		switch a.ClientStatus {
		case "running":
			running++
		case "complete":
			succeeded++
		case "failed", "lost":
			failed++
		}
	}

	region := job.Region
	if region == "" {
		region = "—"
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"  %-30s  %-10s  %-10s  allocs: %d running / %d succeeded / %d failed\n",
		job.ID, job.Status, region, running, succeeded, failed)

	if showMetrics, _ := cmd.Flags().GetBool("metrics"); showMetrics && running > 0 {
		printAllocMetrics(cmd, jobID)
	}

	switch job.Status {
	case "complete":
		if failed > 0 {
			os.Exit(1)
		}
		os.Exit(0)
	case "dead":
		os.Exit(1)
	case "running", "pending":
		os.Exit(2)
	default:
		os.Exit(2)
	}
	return nil
}

// printAllocMetrics queries Prometheus for per-alloc CPU and memory metrics
// and prints a compact table below the job status line.
func printAllocMetrics(cmd *cobra.Command, jobID string) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	ctx := cfg.ActiveCtx()

	promHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "prometheus", "http")
	if !ok || promHTTP == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "  (--metrics: prometheus not configured; run abc cluster capabilities sync)\n")
		return
	}

	pc := floor.NewPrometheusClient(promHTTP)
	cmdCtx := cmd.Context()

	cpuMetrics, errCPU := pc.Query(cmdCtx,
		fmt.Sprintf(`nomad_client_allocs_cpu_total_percent{exported_job=%q}`, jobID))
	memMetrics, errMem := pc.Query(cmdCtx,
		fmt.Sprintf(`nomad_client_allocs_memory_rss{exported_job=%q}`, jobID))

	if errCPU != nil && errMem != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "  (--metrics: %v)\n", errCPU)
		return
	}

	// Index by alloc_id.
	cpuByAlloc := make(map[string]float64)
	for _, m := range cpuMetrics {
		cpuByAlloc[m.Labels["alloc_id"]] = m.Value
	}
	memByAlloc := make(map[string]float64)
	taskByAlloc := make(map[string]string)
	for _, m := range memMetrics {
		memByAlloc[m.Labels["alloc_id"]] = m.Value
		taskByAlloc[m.Labels["alloc_id"]] = m.Labels["task"]
	}

	// Collect all alloc IDs.
	seen := make(map[string]bool)
	for _, m := range cpuMetrics {
		seen[m.Labels["alloc_id"]] = true
	}
	for _, m := range memMetrics {
		seen[m.Labels["alloc_id"]] = true
	}
	if len(seen) == 0 {
		return
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  %-14s %-14s %-8s %-12s\n", "ALLOC", "TASK", "CPU %", "MEM RSS")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 52))
	for allocID := range seen {
		short := allocID
		if len(short) > 8 {
			short = short[:8]
		}
		task := taskByAlloc[allocID]
		if task == "" {
			if len(cpuMetrics) > 0 {
				task = cpuMetrics[0].Labels["task"]
			}
		}
		cpu := cpuByAlloc[allocID]
		mem := memByAlloc[allocID]
		memStr := fmt.Sprintf("%.0f MB", mem/1024/1024)
		fmt.Fprintf(out, "  %-14s %-14s %-8.1f %s\n", short, task, cpu, memStr)
	}
	fmt.Fprintln(out)
}
