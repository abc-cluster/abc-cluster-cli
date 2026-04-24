package grafana

import "github.com/spf13/cobra"

// NewCmd returns the "grafana" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grafana",
		Short: "Grafana CLI passthrough helpers",
		Long: `Commands for running local Grafana CLI operations.

  abc admin services grafana cli -- --version
  abc admin services grafana cli -- plugins ls`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
