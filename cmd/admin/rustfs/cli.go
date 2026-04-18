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
		Long:               "Run the local rustfs binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` for verbatim argv to rustfs. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged. When the active context has cluster_type abc-nodes and admin.abc_nodes S3 fields are set, AWS_* defaults are merged only for keys not already set in the process environment.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runRustFSCLI,
	}
	return cmd
}

func runRustFSCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_RUSTFS_CLI_BINARY", "RUSTFS_CLI_BINARY", "RUSTFS_BINARY")
	}

	base := os.Environ()
	if cfg, err := config.Load(); err == nil && cfg != nil {
		base = utils.UpsertEnvOnlyMissing(base, cfg.ActiveCtx().AbcNodesStorageCLIEnv())
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"rustfs"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
