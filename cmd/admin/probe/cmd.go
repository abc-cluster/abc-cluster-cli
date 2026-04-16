// Package probe implements the "abc admin services probe" command group.
//
// The cli subcommand is a passthrough to the local abc-node-probe binary,
// with optional download via cli setup (same managed dir as nomad setup).
package probe

import "github.com/spf13/cobra"

// NewCmd returns the "probe" subcommand group under abc admin services.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "probe",
		Short: "abc-node-probe passthrough helper",
		Long: `Run the abc-node-probe diagnostic binary against your cluster context.

  abc admin services probe cli --help
  abc admin services probe cli setup`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
