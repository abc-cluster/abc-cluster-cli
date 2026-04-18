package probe

import (
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cli [abc-node-probe-args...]",
		Short: "Run the local abc-node-probe CLI",
		Long: `Run the abc-node-probe binary as a passthrough alias. Use "abc admin services probe cli setup"
to install the managed binary into ~/.abc/binaries (or ABC_BINARIES_DIR). Use
--binary-location to select a specific binary (same convention as nomad/tailscale).

Environment overrides when --binary-location is unset:
  ABC_NODE_PROBE_CLI_BINARY, ABC_NODE_PROBE_BINARY, NODE_PROBE_BINARY

Use optional leading "--binary-location <path>" then "--" to pass all following arguments verbatim to abc-node-probe.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runProbeCLI,
	}
	return cmd
}

func runProbeCLI(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && args[0] == "setup" {
		return runProbeCLISetup(cmd)
	}

	binaryLocation, passthrough, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_NODE_PROBE_CLI_BINARY", "ABC_NODE_PROBE_BINARY", "NODE_PROBE_BINARY")
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("abc-node-probe"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	return utils.RunExternalCLI(cmd.Context(), passthrough, binaryLocation, []string{"abc-node-probe"}, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func runProbeCLISetup(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	dir, err := utils.ManagedBinaryDir()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Setting up wrapped binaries in %s\n", dir)
	fmt.Fprintln(out, "Checking PATH first, then downloading missing binaries...")
	if _, err := utils.SetupNodeProbeBinary(out); err != nil {
		return err
	}
	fmt.Fprintln(out, "Setup complete.")
	fmt.Fprintf(out, "Tip: prepend %s to PATH to prefer managed binaries.\n", dir)
	return nil
}
