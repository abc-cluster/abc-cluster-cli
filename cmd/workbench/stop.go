package workbench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the running workbench session",
		Long: `Stop the running workbench session.

For Docker-backend sessions: deregisters the Nomad service job.
For VM-backend sessions: suspends the Multipass VM (state is preserved; resume in ~8s).

Your homedir is always preserved on the cluster node. All installed tools,
notebooks, and files survive the stop.

Run 'abc workbench start' (or 'abc workbench start --backend=vm') to reconnect.`,
		RunE: runStop,
	}
	return cmd
}

func runStop(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()

	user := strings.TrimSpace(ctx.Admin.Whoami)
	if user == "" && ctx.Auth != nil {
		user = strings.TrimSpace(ctx.Auth.Whoami)
	}
	if user == "" {
		return fmt.Errorf("cannot determine user: run 'abc auth whoami'")
	}

	db, err := state.Open()
	if err != nil {
		return fmt.Errorf("open local db: %w", err)
	}

	sess, err := workbench.ActiveSession(context.Background(), db, user)
	if err != nil {
		if errors.Is(err, workbench.ErrNoSession) {
			return fmt.Errorf("no running workbench session for user %q\nuse 'abc workbench status' to check", user)
		}
		return fmt.Errorf("look up session: %w", err)
	}

	// VM sessions have job_id = "wb-<user>"; Docker sessions have "abc-workbench-<user>".
	if strings.HasPrefix(sess.JobID, "wb-") {
		return stopVMSession(cmd, ctx, db, sess)
	}
	return stopDockerSession(cmd, db, sess)
}

// stopVMSession suspends the Multipass VM (preserves in-memory state; ~8s to resume).
func stopVMSession(cmd *cobra.Command, ctx config.Context, db *sql.DB, sess *workbench.Session) error {
	// Resolve platform node from config, defaulting to aither.
	nodeName := "aither"
	if wn, ok := config.GetAdminFloorField(&ctx.Admin.Services, "workbench", "node"); ok && wn != "" {
		nodeName = wn
	}
	node := workbench.NodeSSHFromName(nodeName)

	fmt.Fprintf(cmd.ErrOrStderr(), "Suspending %s...\n", sess.JobID)
	if err := workbench.SuspendVM(node, sess.JobID); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: suspend returned error: %v\n", err)
		fmt.Fprintln(cmd.ErrOrStderr(), "(marking session as stopped in local db anyway)")
	}

	if err := workbench.UpdateStopped(context.Background(), db, sess.SessionID); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not update session record: %v\n", err)
	}

	fmt.Printf("Workbench suspended. Resume with: abc workbench start --backend=vm\n")
	fmt.Printf("Your files are preserved at ~/  inside %s.\n", sess.JobID)
	return nil
}

// stopDockerSession deregisters the Nomad service job.
func stopDockerSession(cmd *cobra.Command, db *sql.DB, sess *workbench.Session) error {
	nc := utils.NomadClientFromConfig().WithNamespace(sess.Namespace)
	_, stopErr := nc.StopJob(context.Background(), sess.JobID, sess.Namespace, false)
	if stopErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: Nomad stop returned error: %v\n", stopErr)
		fmt.Fprintln(cmd.ErrOrStderr(), "(marking session as stopped in local db anyway)")
	}

	if err := workbench.UpdateStopped(context.Background(), db, sess.SessionID); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not update session record: %v\n", err)
	}

	fmt.Printf("Session stopped. Your homedir is preserved at /data/workbench/%s/home/.\n", sess.User)
	return nil
}
