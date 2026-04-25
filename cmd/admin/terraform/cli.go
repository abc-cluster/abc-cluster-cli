package terraform

import (
	"os"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "cli [terraform-args...]",
		Short:              "Run the local Terraform CLI",
		Long:               "Run the local terraform binary as a passthrough alias. Optional leading `--binary-location <path>`; use `--` to pass the following argv verbatim to terraform. Without `--`, all arguments after any leading `--binary-location` pairs are passed through unchanged.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runTerraformCLI,
	}
	return cmd
}

func runTerraformCLI(cmd *cobra.Command, args []string) error {
	binaryLocation, passthroughArgs, err := utils.ExtractBinaryLocationFlag(args)
	if err != nil {
		return err
	}
	if binaryLocation == "" {
		binaryLocation = utils.EnvOrDefault(
			"ABC_TERRAFORM_CLI_BINARY",
			"TERRAFORM_CLI_BINARY",
			"TERRAFORM_BINARY",
		)
		if binaryLocation == "" {
			if managedPath, mErr := utils.ManagedBinaryPath("terraform"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	return utils.RunExternalCLI(cmd.Context(), passthroughArgs, binaryLocation, []string{"terraform"}, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
