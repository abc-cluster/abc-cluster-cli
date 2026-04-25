package eget

import "github.com/spf13/cobra"

// NewCmd returns the "eget" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eget",
		Short: "eget CLI passthrough helpers",
		Long: `Commands for running local eget CLI operations.

  abc admin services eget cli -- --help
  abc admin services eget cli -- zyedidia/eget`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
