package nomadpack

import (
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
		RunE:               runNomadPackCLI,
	}
	return cmd
}

func runNomadPackCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_NOMAD_PACK_CLI_BINARY",
			"NOMAD_PACK_CLI_BINARY",
			"NOMAD_PACK_BINARY",
		)
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"nomad-pack"}, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}
