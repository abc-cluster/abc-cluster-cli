package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show an app's status and details",
		Args:  cobra.ExactArgs(1),
		RunE:  runShow,
	}
}

func runShow(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	r, err := resolveApp(ctx, nc, args[0])
	if err != nil {
		return err
	}
	job := r.Job

	fmt.Fprintf(out, "  App        %s\n", r.Name)
	fmt.Fprintf(out, "  Project    %s\n", orDash(r.Project))
	fmt.Fprintf(out, "  Framework  %s\n", orDash(r.Framework))
	fmt.Fprintf(out, "  URL        %s\n", appURLFromJob(r))
	fmt.Fprintf(out, "  Job        %s (namespace %s)\n", r.JobID, nc.DefaultNamespace())
	fmt.Fprintf(out, "  Image      %s\n", orDash(taskImage(job)))

	// Declared resource limits + health path + data buckets — read from job
	// meta, which the generator stamps so show needn't re-read abc-app.yaml.
	cpu := job.Meta["abc_cpu"]
	mem := job.Meta["abc_memory"]
	if cpu != "" || mem != "" {
		fmt.Fprintf(out, "  Resources  cpu=%sMHz memory=%sMiB\n", orZero(cpu), orZero(mem))
	}
	if h := job.Meta["abc_health"]; h != "" {
		fmt.Fprintf(out, "  Health     %s\n", h)
	}
	if b := job.Meta["abc_data_buckets"]; b != "" {
		fmt.Fprintf(out, "  Data       %s\n", strings.ReplaceAll(b, ",", ", "))
	}
	if job.SubmitTime > 0 {
		fmt.Fprintf(out, "  Deployed   %s\n", time.Unix(0, job.SubmitTime).Format(time.RFC3339))
	}

	// Alloc + Nomad-native health-check status; best-effort live stats.
	allocs, err := nc.GetJobAllocs(ctx, r.JobID, nc.DefaultNamespace(), false)
	if err != nil || len(allocs) == 0 {
		fmt.Fprintln(out, "  Alloc      (none running)")
		return nil
	}
	var running *utils.NomadAllocStub
	for i := range allocs {
		if allocs[i].ClientStatus == "running" {
			running = &allocs[i]
			break
		}
	}
	if running == nil {
		running = &allocs[0]
	}
	fmt.Fprintf(out, "  Alloc      %s (%s)\n", running.ID, running.ClientStatus)

	if checks, cerr := nc.GetAllocChecks(ctx, running.ID); cerr == nil {
		status := "unknown"
		for _, c := range checks {
			status = c.Status
			break
		}
		fmt.Fprintf(out, "  HealthChk  %s\n", status)
	}

	// Live CPU/mem — best-effort; omit on any error (do not fail the command).
	if running.ClientStatus == "running" {
		if stats, serr := nc.GetAllocStats(ctx, running.ID); serr == nil {
			memMiB := stats.ResourceUsage.MemoryStats.RSS / (1024 * 1024)
			fmt.Fprintf(out, "  Live       cpu=%.1f%% memory=%dMiB\n",
				stats.ResourceUsage.CpuStats.Percent, memMiB)
		}
	}
	return nil
}

// orZero returns "0" for an empty string, otherwise the value.
func orZero(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}
