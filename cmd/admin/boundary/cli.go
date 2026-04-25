package boundary

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [boundary-args...]",
		Short:              "Run the local Boundary CLI",
		Long:               "Run the local boundary binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to boundary. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runBoundaryCLI,
	}
	return cmd
}

func runBoundaryCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_BOUNDARY_CLI_BINARY",
			"BOUNDARY_CLI_BINARY",
			"BOUNDARY_BINARY",
		)
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("boundary"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"boundary"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
