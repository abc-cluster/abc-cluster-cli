package eget

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [eget-args...]",
		Short:              "Run the local eget CLI",
		Long:               "Run the local eget binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to eget. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runEgetCLI,
	}
	return cmd
}

func runEgetCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_EGET_CLI_BINARY",
			"EGET_CLI_BINARY",
			"EGET_BINARY",
		)
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("eget"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"eget"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
