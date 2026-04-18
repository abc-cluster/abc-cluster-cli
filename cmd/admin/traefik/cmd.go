package traefik

import "github.com/spf13/cobra"

// NewCmd returns the "traefik" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "traefik",
		Short: "Traefik CLI passthrough helpers",
		Long: `Commands for running the local Traefik binary (same argv as upstream).

  abc admin services traefik cli version
  abc admin services traefik cli healthcheck --help`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
