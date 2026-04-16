// Package rclone implements the "abc admin services rclone" command group.
//
// The cli subcommand is a passthrough to the local rclone binary, optionally
// downloading it via cli setup like other admin service CLI wrappers.
package rclone

import "github.com/spf13/cobra"

// NewCmd returns the "rclone" subcommand group under abc admin services.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rclone",
		Short: "Rclone passthrough helper",
		Long: `Run the rclone data mover against remotes defined for your workspace.

  abc admin services rclone cli version
  abc admin services rclone cli --abc-server-config about
  abc admin services rclone cli setup`,
	}

	cmd.AddCommand(newCLICmd())
	return cmd
}
