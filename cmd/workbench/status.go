package workbench

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current workbench session status",
		RunE:  runStatus,
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
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

	sess, err := workbench.LatestSession(context.Background(), db, user)
	if err != nil {
		if errors.Is(err, workbench.ErrNoSession) {
			// No Docker/VM session in local.db — user is on JupyterHub.
			// Show the hub URL; server status is visible in the hub UI.
			printHubStatus(cmd, ctx, "check")
			return nil
		}
		return fmt.Errorf("look up session: %w", err)
	}

	started := "(unknown)"
	if sess.StartedAt > 0 {
		started = time.Unix(sess.StartedAt, 0).Format("2006-01-02 15:04:05")
	}
	idle := "(unknown)"
	if sess.LastSeenAt > 0 {
		elapsed := time.Since(time.Unix(sess.LastSeenAt, 0)).Round(time.Minute)
		idle = elapsed.String()
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Session:   %s\n", sess.SessionID)
	fmt.Fprintf(out, "Status:    %s\n", sess.Status)
	fmt.Fprintf(out, "IDE:       %s\n", sess.IDE)
	fmt.Fprintf(out, "Cores:     %d\n", sess.Cores)
	fmt.Fprintf(out, "Memory:    %d MB\n", sess.MemoryMB)
	fmt.Fprintf(out, "Started:   %s\n", started)
	fmt.Fprintf(out, "Idle:      %s\n", idle)
	if sess.Status == "running" && sess.Host != "" && sess.Port > 0 {
		fmt.Fprintf(out, "URL:       http://%s:%d\n", sess.Host, sess.Port)
	}
	return nil
}
