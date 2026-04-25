package boundary

import "github.com/spf13/cobra"

// NewCmd returns the "boundary" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "boundary",
		Short: "Boundary CLI passthrough helpers",
		Long: `Commands for running local Boundary CLI operations.

  abc admin services boundary cli -- --version
  abc admin services boundary cli -- targets list -scope-id global`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
