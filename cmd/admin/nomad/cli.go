package nomad

import (
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [nomad-args...]",
		Short:              "Run the local Nomad CLI with abc context defaults",
		Long:               "Run the local nomad binary as a preconfigured alias. Nomad address, token, and region are resolved from the active abc config context when not provided via flags. Use `abc admin services nomad cli setup` to install wrapped binaries into ~/.abc/binaries. Use --binary-location to select a specific nomad binary.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runNomadCLI,
	}
	return cmd
}

func runNomadCLI(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && args[0] == "setup" {
		return runNomadCLISetup(cmd)
	}

	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_NOMAD_CLI_BINARY", "NOMAD_CLI_BINARY", "NOMAD_BINARY")
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("nomad"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	addr, token, region := nomadConnectionFromCmd(cmd)
	return utils.RunNomadCLI(cmd.Context(), passthroughArgs, binaryLocation, addr, token, region, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func runNomadCLISetup(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	dir, err := utils.ManagedBinaryDir()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Setting up wrapped binaries in %s\n", dir)
	fmt.Fprintln(out, "Checking PATH first, then downloading missing binaries...")
	if _, err := utils.SetupNomadAndProbeBinaries(out); err != nil {
		return err
	}
	fmt.Fprintln(out, "Setup complete.")
	fmt.Fprintf(out, "Tip: prepend %s to PATH to prefer managed binaries.\n", dir)
	return nil
}

func nomadConnectionFromCmd(cmd *cobra.Command) (addr, token, region string) {
	addr, token, region = nomadDefaultsFromConfigFirst()

	if cmd.Flags().Changed("nomad-addr") {
		addr, _ = cmd.Flags().GetString("nomad-addr")
	} else if addr == "" {
		addr = utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR")
	}

	if cmd.Flags().Changed("nomad-token") {
		token, _ = cmd.Flags().GetString("nomad-token")
	} else if token == "" {
		token = utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN")
	}

	if cmd.Flags().Changed("region") {
		region, _ = cmd.Flags().GetString("region")
	} else if region == "" {
		region = utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION")
	}
	return addr, token, region
}

func nomadDefaultsFromConfigFirst() (addr, token, region string) {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "", "", ""
	}
	active := cfg.ActiveCtx()
	return active.NomadAddr(), active.NomadToken(), active.NomadRegion()
}
