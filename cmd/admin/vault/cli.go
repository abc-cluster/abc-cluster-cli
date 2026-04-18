package vault

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [vault-args...]",
		Short:              "Run the local Vault or OpenBao (bao) CLI",
		Long:               "Run the local vault or OpenBao (`bao`) binary as a passthrough alias. Use optional leading `--binary-location <path>` then `--` to pass all following arguments verbatim to the underlying binary. When the active context has cluster_type abc-nodes, VAULT_ADDR / VAULT_TOKEN merge from admin.services.vault.http and admin.services.vault.access_key (optional dev token) — only for keys not already set in the process environment.",
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

	base := os.Environ()
	if cfg, err := config.Load(); err == nil && cfg != nil {
		base = utils.UpsertEnvOnlyMissing(base, cfg.ActiveCtx().AbcNodesVaultCLIEnv())
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"vault", "bao", "openbao"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
