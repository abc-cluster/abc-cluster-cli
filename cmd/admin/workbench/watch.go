package workbench

import (
	"fmt"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Idle-session reaper: poll JupyterHub and stop servers that have been idle too long",
		Long: `Poll the JupyterHub admin API at a regular interval and stop servers that
have been idle longer than --idle-timeout.

Runs forever unless --once is given. Safe to run as a systemd unit or cron job.
Users' work is always preserved — the persistent home dir at
/data/workbench/<slot>/home/ is unaffected by a server stop.

Examples:

  # Reap sessions idle for more than 2 hours, check every 60 seconds:
  abc admin services workbench watch

  # Dry-run to see what would be stopped without stopping anything:
  abc admin services workbench watch --dry-run

  # One-shot check (useful from cron):
  abc admin services workbench watch --once --idle-timeout 4h`,
		RunE: runWatch,
	}

	cmd.Flags().Duration("idle-timeout", 2*time.Hour, "Stop servers idle longer than this duration")
	cmd.Flags().Duration("interval", 60*time.Second, "Poll interval (ignored when --once is set)")
	cmd.Flags().String("node", "", "SSH host alias for the platform node (overrides config; default: sun-<node> from config or sun-aither)")
	cmd.Flags().Bool("dry-run", false, "Report what would be stopped without actually stopping servers")
	cmd.Flags().Bool("once", false, "Run one check then exit (instead of looping forever)")

	return cmd
}

func runWatch(cmd *cobra.Command, args []string) error {
	idleTimeout, _ := cmd.Flags().GetDuration("idle-timeout")
	interval, _ := cmd.Flags().GetDuration("interval")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	once, _ := cmd.Flags().GetBool("once")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	hub, err := hubClientFromCtx(actx)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "[dry-run] No servers will be stopped.")
	}

	for {
		if err := watchTick(cmd, hub, idleTimeout, dryRun); err != nil {
			// Log but don't exit — transient Hub API errors shouldn't kill the watcher.
			fmt.Fprintf(cmd.ErrOrStderr(), "[watch] tick error: %v\n", err)
		}

		if once {
			break
		}

		select {
		case <-cmd.Context().Done():
			return nil
		case <-time.After(interval):
		}
	}
	return nil
}

// watchTick runs one poll/stop cycle.
func watchTick(
	cmd *cobra.Command,
	hub *HubClient,
	idleTimeout time.Duration,
	dryRun bool,
) error {
	users, err := hub.ListActiveUsers(cmd.Context())
	if err != nil {
		return fmt.Errorf("list active users: %w", err)
	}

	now := time.Now()
	checked := 0
	stopped := 0

	for _, u := range users {
		if !u.IsRunning() {
			continue
		}
		checked++

		idle := u.ServerIdleSince()
		if idle < idleTimeout {
			continue
		}

		fmt.Fprintf(cmd.OutOrStdout(),
			"[%s] slot %q idle for %s (threshold %s)",
			now.Format("2006-01-02 15:04:05"), u.Name,
			idle.Truncate(time.Second), idleTimeout,
		)

		if dryRun {
			fmt.Fprintln(cmd.OutOrStdout(), " — [dry-run, would stop]")
			continue
		}

		if err := hub.StopServer(cmd.Context(), u.Name); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), " — stop error: %v\n", err)
			continue
		}
		fmt.Fprintln(cmd.OutOrStdout(), " — stopped")
		stopped++
	}

	if checked > 0 || stopped > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"[%s] checked %d active sessions, stopped %d\n",
			now.Format("2006-01-02 15:04:05"), checked, stopped,
		)
	}
	return nil
}
