package tailscale

import "github.com/spf13/cobra"

// NewCmd returns the "tailscale" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tailscale",
		Short: "Tailscale passthrough helpers",
		Long: `Commands for running Tailscale CLI operations.

  abc admin services tailscale cli status
  abc admin services tailscale cli ip -4`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
