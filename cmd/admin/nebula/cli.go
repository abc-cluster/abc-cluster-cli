package nebula

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [nebula-args...]",
		Short:              "Run the local Nebula CLI",
		Long:               "Run the local nebula binary as a passthrough alias. Use optional leading `--binary-location <path>` then `--` to pass all following arguments verbatim to nebula.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runNebulaCLI,
	}
	return cmd
}

func runNebulaCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_NEBULA_CLI_BINARY", "NEBULA_CLI_BINARY", "NEBULA_BINARY")
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"nebula"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
