// Package workbench implements the "abc workbench" command group.
//
// abc workbench start  — launch an interactive session (Docker, Nomad service job)
// abc workbench stop   — stop the running session
// abc workbench status — print session status table
// abc workbench url    — print the IDE URL and password
// abc workbench logs   — stream session logs
//
// Sessions are tracked in ~/.abc/local.db (workbench_sessions table,
// migration 0013). The Nomad job name is "abc-workbench-<user>"; the
// session_id is a WB-prefixed ULID.
package workbench

import "github.com/spf13/cobra"

// NewCmd returns the "abc workbench" parent command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workbench",
		Short: "Manage interactive workbench sessions",
		Long: `Start and manage an interactive workbench session on the active cluster.

A session is a Nomad service job running a code-server IDE with your
homedir bind-mounted for persistence. Sessions survive disconnect —
reconnect to the same URL. Use 'abc workbench stop' to release resources.

  abc workbench start          Start a session (code-server, 2 cores, 4 GB)
  abc workbench start --cores 4 --mem 8192
  abc workbench stop           Stop the running session
  abc workbench status         Show session status
  abc workbench url            Print IDE URL and password
  abc workbench logs           Stream session logs`,
	}

	cmd.AddCommand(newStartCmd())
	cmd.AddCommand(newStopCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newURLCmd())
	cmd.AddCommand(newLogsCmd())
	return cmd
}
