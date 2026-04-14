package tailscale

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [tailscale-args...]",
		Short:              "Run the local Tailscale CLI",
		Long:               "Run the local tailscale binary as a passthrough alias. Use --binary-location to select a specific binary.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runTailscaleCLI,
	}
	return cmd
}

func runTailscaleCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_TAILSCALE_CLI_BINARY", "TAILSCALE_CLI_BINARY", "TAILSCALE_BINARY")
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"tailscale"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
