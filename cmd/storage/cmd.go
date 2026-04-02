package storage

import "github.com/spf13/cobra"

// NewCmd returns the "storage" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Inspect storage inventory and sizes",
		Long:  `Commands for querying storage capacities, usage, and available mount endpoints.`,
	}
	cmd.AddCommand(newSizeCmd())
	return cmd
}
