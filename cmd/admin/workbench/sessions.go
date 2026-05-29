package workbench

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List active JupyterHub sessions with idle time",
		Long: `Query the JupyterHub admin API and list all users with a running server.

Prints slot name, server start time, last-activity time, and how long the
server has been idle.

  abc admin services workbench sessions`,
		RunE: runSessions,
	}
	return cmd
}

func runSessions(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	hub, err := hubClientFromCtx(actx)
	if err != nil {
		return err
	}

	users, err := hub.ListActiveUsers(cmd.Context())
	if err != nil {
		return fmt.Errorf("list active users: %w", err)
	}

	if len(users) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No active sessions.")
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLOT\tSTARTED\tLAST ACTIVITY\tIDLE")
	for _, u := range users {
		started := "-"
		if u.Server != nil && u.Server.Started != nil {
			started = u.Server.Started.Local().Format("2006-01-02 15:04")
		}
		lastAct := "-"
		idle := "-"
		if u.Server != nil && u.Server.LastActivity != nil {
			lastAct = u.Server.LastActivity.Local().Format("2006-01-02 15:04")
			d := time.Since(*u.Server.LastActivity).Truncate(time.Minute)
			idle = d.String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", u.Name, started, lastAct, idle)
	}
	return w.Flush()
}
