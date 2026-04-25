package hashiup

import "github.com/spf13/cobra"

// NewCmd returns the "hashi-up" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hashi-up",
		Short: "hashi-up CLI passthrough helpers",
		Long: `Commands for running local hashi-up CLI operations.

  abc admin services hashi-up cli -- --help
  abc admin services hashi-up cli -- consul server -n 1`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
