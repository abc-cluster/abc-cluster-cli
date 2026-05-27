package workbench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var follow bool

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Stream logs from the running workbench session",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, follow)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream logs continuously")
	return cmd
}

func runLogs(cmd *cobra.Command, follow bool) error {
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
			return fmt.Errorf("no running workbench session — use 'abc workbench start' first")
		}
		return fmt.Errorf("look up session: %w", err)
	}

	nc := utils.NomadClientFromConfig().WithNamespace(sess.Namespace)

	allocs, err := nc.GetJobAllocs(context.Background(), sess.JobID, sess.Namespace, false)
	if err != nil {
		return fmt.Errorf("list allocations: %w", err)
	}

	var allocID string
	for _, a := range allocs {
		if a.ClientStatus == "running" {
			allocID = a.ID
			break
		}
	}
	if allocID == "" {
		return fmt.Errorf("no running allocation found for session %s", sess.SessionID)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "Streaming logs for alloc %s (task: session)...\n", allocID[:8])

	// Stream stdout then stderr (or follow stdout).
	bctx := context.Background()
	if follow {
		_, err = nc.StreamLogs(bctx, allocID, "session", "stdout", "start", 0, true, os.Stdout)
	} else {
		_, _ = nc.StreamLogs(bctx, allocID, "session", "stdout", "start", 0, false, os.Stdout)
		_, err = nc.StreamLogs(bctx, allocID, "session", "stderr", "start", 0, false, os.Stderr)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("stream logs: %w", err)
	}
	return nil
}
