package tailscale

import (
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [tailscale-args...]",
		Short:              "Run the local Tailscale CLI",
		Long:               "Run the local tailscale binary as a passthrough alias. Use `abc admin services tailscale cli setup` to install wrapped binaries into ~/.abc/binaries. Use --binary-location to select a specific binary.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runTailscaleCLI,
	}
	return cmd
}

func runTailscaleCLI(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && args[0] == "setup" {
		return runTailscaleCLISetup(cmd)
	}

	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_TAILSCALE_CLI_BINARY", "TAILSCALE_CLI_BINARY", "TAILSCALE_BINARY")
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("tailscale"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"tailscale"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func runTailscaleCLISetup(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	dir, err := utils.ManagedBinaryDir()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Setting up wrapped binaries in %s\n", dir)
	fmt.Fprintln(out, "Checking PATH first, then downloading missing binaries...")
	if _, err := utils.SetupTailscaleBinary(out); err != nil {
		return err
	}
	fmt.Fprintln(out, "Setup complete.")
	fmt.Fprintf(out, "Tip: prepend %s to PATH to prefer managed binaries.\n", dir)
	return nil
}
