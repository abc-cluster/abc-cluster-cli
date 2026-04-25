package nomadpack

import "github.com/spf13/cobra"

// NewCmd returns the "nomad-pack" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nomad-pack",
		Short: "Nomad Pack passthrough CLI",
		Long: `Commands for running nomad-pack CLI operations.

  abc admin services nomad-pack cli -- version
  abc admin services nomad-pack cli -- run deployments/abc-nodes/nomad-packs/abc_nodes_enhanced`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
