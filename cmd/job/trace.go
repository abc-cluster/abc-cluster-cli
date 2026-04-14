package job

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newTraceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace <job-id>",
		Short: "Show a detailed execution trace for a job",
		Long: `Display a structured execution trace for a Nomad job, including
allocation placement, task lifecycle events, resource usage, and log excerpts.

  abc job trace nextflow-head-abc123`,
		Args: cobra.ExactArgs(1),
		RunE: runTrace,
	}
	cmd.Flags().Bool("json", false, "Output trace as JSON")
	cmd.Flags().String("alloc", "", "Restrict trace to a specific allocation ID")
	return cmd
}

func runTrace(_ *cobra.Command, args []string) error {
	jobID := args[0]
	fmt.Printf("  abc job trace %q: not yet implemented.\n", jobID)
	return nil
}
