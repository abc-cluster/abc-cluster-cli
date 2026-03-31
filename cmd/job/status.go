package job

import (
	"fmt"
	"os"

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
	return cmd
}

func runStatus(cmd *cobra.Command, args []string) error {
	jobID := args[0]
	ns, _ := cmd.Flags().GetString("namespace")
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
