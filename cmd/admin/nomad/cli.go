package nomad

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [nomad-args...]",
		Short:              "Run the local Nomad CLI with abc context defaults",
		Long:               "Run the local nomad binary as a preconfigured alias. Nomad address, token, and region are resolved from the active abc config context when not provided via flags. Use --binary-location to select a specific nomad binary.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runNomadCLI,
	}
	return cmd
}

func runNomadCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_NOMAD_CLI_BINARY", "NOMAD_CLI_BINARY", "NOMAD_BINARY")
	}

	addr, token, region := nomadConnectionFromCmd(cmd)
	return utils.RunNomadCLI(cmd.Context(), passthroughArgs, binaryLocation, addr, token, region, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func nomadConnectionFromCmd(cmd *cobra.Command) (addr, token, region string) {
	addr, _ = cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	token, _ = cmd.Flags().GetString("nomad-token")
	if token == "" {
		token, _ = cmd.Root().PersistentFlags().GetString("nomad-token")
	}
	region, _ = cmd.Flags().GetString("region")
	if region == "" {
		region, _ = cmd.Root().PersistentFlags().GetString("region")
	}
	if addr == "" || token == "" || region == "" {
		cfgAddr, cfgToken, cfgRegion := utils.NomadDefaultsFromConfig()
		if addr == "" {
			addr = cfgAddr
		}
		if token == "" {
			token = cfgToken
		}
		if region == "" {
			region = cfgRegion
		}
	}
	return addr, token, region
}
