package vault

import "github.com/spf13/cobra"

// NewCmd returns the "vault" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Vault and OpenBao (bao) passthrough helpers",
		Long: `Commands for running Vault and OpenBao CLI operations (OpenBao's binary is named bao).

  abc admin services vault cli status
  abc admin services vault cli secrets list

For abc-nodes contexts, run abc admin services config sync (and optionally set
admin.services.vault.access_key to your lab root token) so VAULT_ADDR / VAULT_TOKEN merge into the CLI.`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
