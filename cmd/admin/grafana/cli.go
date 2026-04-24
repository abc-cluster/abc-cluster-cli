package grafana

import (
	"path/filepath"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [grafana-cli-args...]",
		Short:              "Run the local Grafana CLI",
		Long:               "Run the local Grafana CLI as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to Grafana CLI. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runGrafanaCLI,
	}
	return cmd
}

func runGrafanaCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_GRAFANA_CLI_BINARY",
			"GRAFANA_CLI_BINARY",
			"GRAFANA_BINARY",
		)
	}

	// The grafana binary requires a leading "cli" subcommand, while grafana-cli does not.
	if needsGrafanaCLIPrefix(binaryLocation) {
		passthroughArgs = append([]string{"cli"}, passthroughArgs...)
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"grafana-cli", "grafana"}, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func needsGrafanaCLIPrefix(binaryLocation string) bool {
	base := filepath.Base(binaryLocation)
	return base == "grafana"
}
