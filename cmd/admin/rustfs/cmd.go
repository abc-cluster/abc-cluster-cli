package rustfs

import "github.com/spf13/cobra"

// NewCmd returns the "rustfs" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rustfs",
		Short: "RustFS passthrough helpers",
		Long: `Commands for running RustFS CLI operations.

  abc admin services rustfs cli status
  abc admin services rustfs cli bucket list`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
