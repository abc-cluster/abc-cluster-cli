package job

import "github.com/spf13/cobra"

// NewCmd returns the "job" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage batch jobs",
		Long:  `Commands for managing and running batch jobs on the abc-cluster platform.`,
	}
	cmd.AddCommand(newRunCmd())
	return cmd
}
