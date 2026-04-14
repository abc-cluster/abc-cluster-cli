package vault

import "github.com/spf13/cobra"

// NewCmd returns the "vault" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Vault/OpenBao passthrough helpers",
		Long: `Commands for running Vault and OpenBao CLI operations.

  abc admin services vault cli status
  abc admin services vault cli secrets list`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
