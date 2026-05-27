package workbench

import (
	"context"
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
		Long: `Stop the running workbench session by deregistering the Nomad service job.

Your homedir (/home/<user>/ inside the session) is preserved on the cluster
node. All installed tools, notebooks, and files survive the stop.

Run 'abc workbench start' to reconnect with the same environment.`,
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

	nc := utils.NomadClientFromConfig().WithNamespace(sess.Namespace)
	_, stopErr := nc.StopJob(context.Background(), sess.JobID, sess.Namespace, false)
	if stopErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: Nomad stop returned error: %v\n", stopErr)
		fmt.Fprintln(cmd.ErrOrStderr(), "(marking session as stopped in local db anyway)")
	}

	if err := workbench.UpdateStopped(context.Background(), db, sess.SessionID); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not update session record: %v\n", err)
	}

	fmt.Printf("Session stopped. Your homedir is preserved at /data/home/%s/.\n", user)
	return nil
}
