package ntfy

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [ntfy-args...]",
		Short:              "Run the local ntfy CLI",
		Long:               "Run the local ntfy binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to ntfy. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runNtfyCLI,
	}
	return cmd
}

func runNtfyCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_NTFY_CLI_BINARY",
			"NTFY_CLI_BINARY",
			"NTFY_BINARY",
		)
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"ntfy"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
