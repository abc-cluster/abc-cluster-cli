package nebula

import "github.com/spf13/cobra"

// NewCmd returns the "nebula" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nebula",
		Short: "Nebula passthrough helpers",
		Long: `Commands for running Nebula CLI operations.

  abc admin services nebula cli -version
  abc admin services nebula cli -config /etc/nebula/config.yml`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
