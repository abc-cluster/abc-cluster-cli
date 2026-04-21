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
		Long:               "Run the local MinIO client binary as a passthrough alias. Defaults to mcli, then mc. Optional leading `--binary-location <path>` and `--config local|nomad|vault` (default local); use `--` to pass the following argv verbatim to mc/mcli (e.g. `... minio cli -- --help`). Without `--`, all arguments after any leading `--binary-location` / `--config` pairs are passed through unchanged. AWS_* / MINIO_ROOT_* defaults resolve from contexts.<name>.admin.services.minio (cred_source + top-level fields; admin.services.nomad is unchanged), then merge only for keys not already set in the process environment.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runMinioCLI,
	}
	return cmd
}

func runMinioCLI(cmd *cobra.Command, args []string) error {
	configSelection, binaryLocation, passthroughArgs, err := utils.ParseAdminServiceCLIArgs(args, true)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_MINIO_CLI_BINARY", "MINIO_CLI_BINARY", "MCLI_BINARY", "MC_BINARY")
	}

	base := os.Environ()
	if cfg, err := config.Load(); err == nil && cfg != nil {
		env, rerr := utils.ResolvedAbcNodesStorageCLIEnv(cmd.Context(), cfg, "minio", configSelection)
		if rerr != nil {
			return rerr
		}
		base = utils.UpsertEnvOnlyMissing(base, env)
	}
	return utils.RunExternalCLIWithEnvAndBase(cmd.Context(), passthroughArgs, binaryLocation, []string{"mcli", "mc"}, base, nil, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
