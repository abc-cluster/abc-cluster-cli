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
		Short: "Print the IDE URL, password, and SSH alias for the running session",
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

	// VM sessions have job_id = "wb-<user>".
	if strings.HasPrefix(sess.JobID, "wb-") {
		return printVMURL(cmd, ctx, sess, user)
	}
	return printDockerURL(cmd, sess, user)
}

// printVMURL prints the stable URL, password, and SSH alias for a VM-backend session.
func printVMURL(cmd *cobra.Command, ctx config.Context, sess *workbench.Session, user string) error {
	// Resolve jump host.
	nodeName := "aither"
	if wn, ok := config.GetAdminFloorField(&ctx.Admin.Services, "workbench", "node"); ok && wn != "" {
		nodeName = wn
	}
	jumpHost := "sun-" + nodeName
	vmName := sess.JobID
	vmIP := sess.Host

	// Path-based URL (available after Caddy routing is configured in abc-seedling-prod).
	workbenchURL := fmt.Sprintf("https://workbench.seedling.abc-cluster.cloud/%s/", user)
	// Direct IP URL (accessible now via Tailscale / aither's network).
	directURL := fmt.Sprintf("http://%s:8080", vmIP)

	sshBlock := workbench.SSHConfigBlock(vmName, vmIP, jumpHost)

	fmt.Fprintf(cmd.OutOrStdout(), `
  Browser:    %s
  Direct:     %s  (via Tailscale or on aither's network)
  Password:   %s  (derived from your MinIO key — always the same)
  Remote SSH: %s

SSH config (in ~/.ssh/config):
%s
Connect from VS Code or Positron:  Remote SSH → %s
`, workbenchURL, directURL, sess.Token, vmName, sshBlock, vmName)

	return nil
}

// printDockerURL prints the URL and password for a Docker-backend session.
// The path-based HTTPS URL is the primary access point (registered in the
// workbench Caddy at session start). The direct Tailscale URL is shown as a
// fallback for users with network access to the platform node.
func printDockerURL(cmd *cobra.Command, sess *workbench.Session, user string) error {
	if sess.Host == "" || sess.Port == 0 {
		return fmt.Errorf("session is starting, port not yet assigned — try again in a moment")
	}
	workbenchURL := fmt.Sprintf("https://workbench.seedling.abc-cluster.cloud/%s/", user)
	directURL := fmt.Sprintf("http://%s:%d", sess.Host, sess.Port)
	fmt.Fprintf(cmd.OutOrStdout(), "  Browser:  %s\n", workbenchURL)
	fmt.Fprintf(cmd.OutOrStdout(), "  Direct:   %s  (Tailscale / platform network only)\n", directURL)
	fmt.Fprintf(cmd.OutOrStdout(), "  Password: %s\n", sess.Token)
	return nil
}
