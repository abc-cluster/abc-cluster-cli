package minio

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [minio-args...]",
		Short:              "Run the local MinIO client CLI",
		Long:               "Run the local MinIO client binary as a passthrough alias. Defaults to mcli, then mc. Use --binary-location to select a specific binary.",
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

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"mcli", "mc"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
