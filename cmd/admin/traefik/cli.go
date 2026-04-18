package traefik

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [traefik-args...]",
		Short:              "Run the local Traefik CLI",
		Long:               "Run the local traefik binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to traefik. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runTraefikCLI,
	}
	return cmd
}

func runTraefikCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		if p, err := utils.ResolveTraefikBinary(); err == nil {
			binaryLocation = p
		} else {
			binaryLocation = utils.EnvOrDefault("ABC_TRAEFIK_CLI_BINARY", "TRAEFIK_CLI_BINARY", "TRAEFIK_BINARY")
		}
	}

	return utils.RunExternalCLI(
		cmd.Context(),
		passthroughArgs,
		binaryLocation,
		[]string{"traefik"},
		os.Stdin,
		cmd.OutOrStdout(),
		cmd.ErrOrStderr(),
	)
}
