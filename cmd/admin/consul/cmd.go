package consul

import "github.com/spf13/cobra"

// NewCmd returns the "consul" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "consul",
		Short: "Consul CLI passthrough helpers",
		Long: `Commands for running local Consul CLI operations.

  abc admin services consul cli -- --version
  abc admin services consul cli -- members`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
