// Package serviceconfig implements `abc admin services config` (floor sync, etc.).
package serviceconfig

import "github.com/spf13/cobra"

// NewCmd returns the `config` subcommand group under `abc admin services`.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Operator config helpers for deployed floor services",
		Long: `Commands that update ~/.abc/config.yaml from live cluster state.

  abc admin services config sync   Populate admin.services.* (and related keys) for abc-nodes contexts`,
	}
	cmd.AddCommand(newSyncCmd())
	return cmd
}
