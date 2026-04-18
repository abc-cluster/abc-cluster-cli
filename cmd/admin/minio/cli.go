package minio

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [minio-args...]",
		Short:              "Run the local MinIO client CLI",
		Long:               "Run the local MinIO client binary as a passthrough alias. Defaults to mcli, then mc. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to mc/mcli (e.g. `... minio cli -- --help`). Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged. When the active context has cluster_type abc-nodes and admin.abc_nodes S3/MinIO fields are set, AWS_* / MINIO_ROOT_* defaults are merged into the environment only for keys not already set in the process (so explicit exports still win).",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runMinioCLI,
	}
	return cmd
}

func runMinioCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_MINIO_CLI_BINARY", "MINIO_CLI_BINARY", "MCLI_BINARY", "MC_BINARY")
	}

	base := os.Environ()
	if cfg, err := config.Load(); err == nil && cfg != nil {
		base = utils.UpsertEnvOnlyMissing(base, cfg.ActiveCtx().AbcNodesStorageCLIEnv())
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"mcli", "mc"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
