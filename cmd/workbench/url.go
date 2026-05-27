package workbench

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

func newURLCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "url",
		Short: "Print the IDE URL and password for the running session",
		RunE:  runURL,
	}
}

func runURL(cmd *cobra.Command, args []string) error {
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

	if sess.Host == "" || sess.Port == 0 {
		return fmt.Errorf("session is starting, port not yet assigned — try again in a moment")
	}

	fmt.Fprintf(cmd.OutOrStdout(), "http://%s:%d\n", sess.Host, sess.Port)
	fmt.Fprintf(cmd.OutOrStdout(), "password: %s\n", sess.Token)
	return nil
}
