package workbench

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	wbinternal "github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Idle-session reaper: poll JupyterHub and stop servers that have been idle too long",
		Long: `Poll the JupyterHub admin API at a regular interval. For every user whose
server has been idle longer than --idle-timeout:

  1. Import their atuin shell history into local state.db (unless --no-import-history).
  2. Stop the server via DELETE /hub/api/users/<slot>/server.

Runs forever unless --once is given. Safe to run as a systemd unit or cron job.

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
	cmd.Flags().Bool("no-import-history", false, "Skip atuin history import before stopping")

	return cmd
}

func runWatch(cmd *cobra.Command, args []string) error {
	idleTimeout, _ := cmd.Flags().GetDuration("idle-timeout")
	interval, _ := cmd.Flags().GetDuration("interval")
	nodeOverride, _ := cmd.Flags().GetString("node")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	once, _ := cmd.Flags().GetBool("once")
	noImport, _ := cmd.Flags().GetBool("no-import-history")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	actx := cfg.ActiveCtx()

	hub, err := hubClientFromCtx(actx)
	if err != nil {
		return err
	}
	node := nodeFromCtx(actx, nodeOverride)

	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local db: %w", err)
	}
	defer db.Close()

	if dryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "[dry-run] No servers will be stopped.")
	}

	for {
		if err := watchTick(cmd, hub, node, db, idleTimeout, dryRun, !noImport); err != nil {
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
	hub *wbinternal.HubClient,
	node wbinternal.NodeSSH,
	db *sql.DB,
	idleTimeout time.Duration,
	dryRun bool,
	importHistory bool,
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

		// Import atuin history before stopping so we don't lose the session's commands.
		if importHistory {
			if err := doImportHistory(cmd, node, db, u.Name); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "\n  warning: atuin import for %q: %v\n", u.Name, err)
			}
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

// doImportHistory imports atuin history for one slot and prints a brief summary.
func doImportHistory(cmd *cobra.Command, node wbinternal.NodeSSH, db *sql.DB, slot string) error {
	rows, err := wbinternal.ReadAtuinFromNode(node, slot)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	already, err := state.ImportedAtuinIDs(db, slot)
	if err != nil {
		return err
	}

	cmds, skipped := wbinternal.AtuinRowsToCommands(rows, already, slot)
	inserted, err := state.InsertShellCommands(db, cmds)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.ErrOrStderr(),
		"  atuin import for %q: %d inserted, %d skipped\n",
		slot, inserted, skipped,
	)
	return nil
}
