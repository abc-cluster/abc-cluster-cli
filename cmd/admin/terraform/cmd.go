package terraform

import "github.com/spf13/cobra"

// NewCmd returns the "terraform" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terraform",
		Short: "Terraform CLI passthrough helpers",
		Long: `Commands for running local Terraform CLI operations.

  abc admin services terraform cli -- --version
  abc admin services terraform cli -- plan`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
