package postgres

import "github.com/spf13/cobra"

// NewCmd returns the "postgres" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "postgres",
		Short: "PostgreSQL CLI passthrough helpers",
		Long: `Commands for running local PostgreSQL client CLI operations.

  abc admin services postgres cli -- --version
  abc admin services postgres cli -- -h 127.0.0.1 -p 5432 -U postgres`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
