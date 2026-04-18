package vault

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [vault-args...]",
		Short:              "Run the local Vault or OpenBao (bao) CLI",
		Long:               "Run the local vault or OpenBao (`bao`) binary as a passthrough alias. Use optional leading `--binary-location <path>` then `--` to pass all following arguments verbatim to the underlying binary.",
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
			"ABC_BAO_CLI_BINARY",
			"BAO_CLI_BINARY",
			"BAO_BINARY",
			"ABC_OPENBAO_CLI_BINARY",
			"OPENBAO_CLI_BINARY",
			"OPENBAO_BINARY",
		)
	}

	return utils.RunExternalCLI(
		cmd.Context(),
		passthroughArgs,
		binaryLocation,
		[]string{"vault", "bao", "openbao"},
		os.Stdin,
		cmd.OutOrStdout(),
		cmd.ErrOrStderr(),
	)
}
