package pipeline

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Remove a saved pipeline from the cluster",
		Long: `Delete a saved pipeline by name.

By default only the pipeline spec (stored in Nomad Variables) is removed.
Use --with-data to also delete associated MinIO data buckets and
--with-jobs to stop and purge any running or completed Nomad jobs for this pipeline.`,
		Args: cobra.ExactArgs(1),
		RunE: runDelete,
	}
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().Bool("with-data", false, "Also delete associated data buckets (MinIO)")
	cmd.Flags().Bool("with-jobs", false, "Also stop and purge Nomad jobs for this pipeline")
	return cmd
}

func runDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	yes, _ := cmd.Flags().GetBool("yes")
	withData, _ := cmd.Flags().GetBool("with-data")
	withJobs, _ := cmd.Flags().GetBool("with-jobs")

	if !yes {
		extra := ""
		if withData {
			extra += " + data"
		}
		if withJobs {
			extra += " + jobs"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Delete saved pipeline %q%s? [y/N]: ", name, extra)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "  Aborted.")
			return nil
		}
	}

	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)

	if err := deletePipeline(cmd.Context(), nc, name, ns); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Pipeline spec %q deleted.\n", name)

	if withJobs {
		fmt.Fprintf(cmd.OutOrStdout(), "  --with-jobs: job purge not yet implemented (stub).\n")
	}
	if withData {
		fmt.Fprintf(cmd.OutOrStdout(), "  --with-data: data bucket deletion not yet implemented (stub).\n")
	}
	return nil
}
