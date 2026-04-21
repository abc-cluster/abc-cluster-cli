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
		Long:               "Run the local vault or OpenBao (`bao`) binary as a passthrough alias. Use optional leading `--binary-location <path>`, `--config local|nomad` (default local), then `--` to pass all following arguments verbatim to the underlying binary. Vault service credentials use cred_source.local and cred_source.nomad only (not cred_source.vault). When the active context has cluster_type abc-nodes, VAULT_ADDR / VAULT_TOKEN merge from admin.services.vault — only for keys not already set in the process environment.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runVaultCLI,
	}
	return cmd
}

func runVaultCLI(cmd *cobra.Command, args []string) error {
	configSelection, binaryLocation, passthroughArgs, err := utils.ParseAdminServiceCLIArgs(args, false)
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
		env, rerr := utils.ResolvedVaultCLIEnv(cmd.Context(), cfg, configSelection)
		if rerr != nil {
			return rerr
		}
		base = utils.UpsertEnvOnlyMissing(base, env)
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"vault", "bao", "openbao"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
