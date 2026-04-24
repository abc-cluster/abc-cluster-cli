package postgres

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [psql-args...]",
		Short:              "Run the local PostgreSQL client CLI",
		Long:               "Run the local psql binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to psql. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runPostgresCLI,
	}
	return cmd
}

func runPostgresCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_POSTGRES_CLI_BINARY",
			"POSTGRES_CLI_BINARY",
			"PSQL_BINARY",
			"POSTGRES_BINARY",
		)
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"psql"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
