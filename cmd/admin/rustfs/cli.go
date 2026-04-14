package rustfs

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [rustfs-args...]",
		Short:              "Run the local RustFS CLI",
		Long:               "Run the local rustfs binary as a passthrough alias. Use --binary-location to select a specific binary.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runRustFSCLI,
	}
	return cmd
}

func runRustFSCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault("ABC_RUSTFS_CLI_BINARY", "RUSTFS_CLI_BINARY", "RUSTFS_BINARY")
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"rustfs"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
