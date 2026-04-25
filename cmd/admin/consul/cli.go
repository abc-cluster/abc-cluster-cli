package consul

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [consul-args...]",
		Short:              "Run the local Consul CLI",
		Long:               "Run the local consul binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to consul. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runConsulCLI,
	}
	return cmd
}

func runConsulCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_CONSUL_CLI_BINARY",
			"CONSUL_CLI_BINARY",
			"CONSUL_BINARY",
		)
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("consul"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"consul"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
