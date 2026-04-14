package vault

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [vault-args...]",
		Short:              "Run the local Vault/OpenBao CLI",
		Long:               "Run the local vault/openbao binary as a passthrough alias. Use --binary-location to select a specific binary.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runVaultCLI,
	}
	return cmd
}

func runVaultCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_VAULT_CLI_BINARY",
			"VAULT_CLI_BINARY",
			"VAULT_BINARY",
			"ABC_OPENBAO_CLI_BINARY",
			"OPENBAO_CLI_BINARY",
			"OPENBAO_BINARY",
		)
	}

	return utils.RunExternalCLI(
		cmd.Context(),
		passthroughArgs,
		binaryLocation,
		[]string{"vault", "openbao"},
		os.Stdin,
		cmd.OutOrStdout(),
		cmd.ErrOrStderr(),
	)
}
