package nomadpack

import (
	"fmt"
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [nomad-pack-args...]",
		Short:              "Run the local nomad-pack CLI",
		Long:               "Run the local nomad-pack binary as a passthrough alias. Use optional leading `--binary-location <path>` then `--` to pass all following arguments verbatim to nomad-pack. Without `--`, arguments after any leading `--binary-location` pair are still forwarded to nomad-pack.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		Hidden:             true,
		RunE:               runNomadPackCLI,
	}
	return cmd
}

func runNomadPackCLI(cmd *cobra.Command, args []string) error {
	fmt.Fprintln(os.Stderr, "[abc] Use 'abc admin services cli nomad-pack -- <args>' instead (this form is deprecated)")
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	return utils.RunNomadPackCLI(cmd.Context(), passthroughArgs, binaryLocation, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}
