package rustfs

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [rustfs-args...]",
		Short:              "Run the local RustFS CLI",
		Long:               "Run the local rustfs binary as a passthrough alias. Optional leading `--binary-location <path>` and `--config local|nomad|vault` (default local); use `--` for verbatim argv to rustfs. Without `--`, all arguments after any leading flags are passed through unchanged. When the active context has cluster_type abc-nodes, AWS_* defaults merge from admin.services.rustfs (cred_source + top-level fields), else admin.abc_nodes — only for keys not already set in the process environment.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runRustFSCLI,
	}
	return cmd
}

func runRustFSCLI(cmd *cobra.Command, args []string) error {
	configSelection, binaryLocation, passthroughArgs, err := utils.ParseAdminServiceCLIArgs(args, true)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_RUSTFS_CLI_BINARY", "RUSTFS_CLI_BINARY", "RUSTFS_BINARY")
	}

	base := os.Environ()
	if cfg, err := config.Load(); err == nil && cfg != nil {
		env, rerr := utils.ResolvedAbcNodesStorageCLIEnv(cmd.Context(), cfg, "rustfs", configSelection)
		if rerr != nil {
			return rerr
		}
		base = utils.UpsertEnvOnlyMissing(base, env)
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"rustfs"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
