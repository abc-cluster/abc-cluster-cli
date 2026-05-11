package hashiup

import (
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [hashi-up-args...]",
		Short:              "Run the local hashi-up CLI",
		Long:               "Run the local hashi-up binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to hashi-up. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		Hidden:             true,
		RunE:               runHashiUpCLI,
	}
	return cmd
}

func runHashiUpCLI(cmd *cobra.Command, args []string) error {
	fmt.Fprintln(os.Stderr, "[abc] Use 'abc admin services cli hashi-up -- <args>' instead (this form is deprecated)")
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_HASHI_UP_CLI_BINARY",
			"HASHI_UP_CLI_BINARY",
			"HASHI_UP_BINARY",
			"HASHIUP_BINARY",
		)
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("hashi-up"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"hashi-up", "hashiup"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
