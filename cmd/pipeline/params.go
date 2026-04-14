package pipeline

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newParamsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params <name>",
		Short: "Show or validate parameters for a saved pipeline",
		Long: `Display the current parameter schema for a saved pipeline.

With --auto-latest, fetches the latest nf-core schema for the pipeline
repository and merges it with any locally saved overrides.

  abc pipeline params rnaseq
  abc pipeline params rnaseq --auto-latest`,
		Args: cobra.ExactArgs(1),
		RunE: runParams,
	}
	cmd.Flags().Bool("auto-latest", false, "Fetch the latest parameter schema from the upstream repository")
	cmd.Flags().Bool("json", false, "Output params as JSON")
	return cmd
}

func runParams(cmd *cobra.Command, args []string) error {
	name := args[0]
	autoLatest, _ := cmd.Flags().GetBool("auto-latest")
	if autoLatest {
		fmt.Fprintf(cmd.OutOrStdout(), "  abc pipeline params %q --auto-latest: not yet implemented.\n", name)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  abc pipeline params %q: not yet implemented.\n", name)
	return nil
}
