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
		Short: "Print the workbench URL and connection details",
		RunE:  runURL,
	}
}

func runURL(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()
	user := resolveUser(ctx)

	// Try local.db first — handles active admin Docker/VM sessions.
	if user != "" {
		db, dbErr := state.Open()
		if dbErr == nil {
			sess, sessErr := workbench.ActiveSession(context.Background(), db, user)
			if sessErr == nil {
				// Active Docker/VM session found — print its specific URL.
				if strings.HasPrefix(sess.JobID, "wb-") {
					return printVMURL(cmd, ctx, sess, user)
				}
				return printDockerURL(cmd, sess, user)
			}
			if !errors.Is(sessErr, workbench.ErrNoSession) {
				return fmt.Errorf("look up session: %w", sessErr)
			}
		}
	}

	// No active Docker/VM session — print the JupyterHub URL.
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  Browser:  %s\n", hubURL(ctx))
	if user != "" {
		fmt.Fprintf(out, "  Username: %s\n", user)
	}
	fmt.Fprintln(out, "  Log in with your pool username. Your server starts automatically.")
	return nil
}

// printVMURL prints the stable URL, password, and SSH alias for a VM-backend session.
func printVMURL(cmd *cobra.Command, ctx config.Context, sess *workbench.Session, user string) error {
	nodeName := "aither"
	if wn, ok := config.GetAdminFloorField(&ctx.Admin.Services, "workbench", "node"); ok && wn != "" {
		nodeName = wn
	}
	jumpHost := "sun-" + nodeName
	vmName := sess.JobID
	vmIP := sess.Host

	workbenchURL := fmt.Sprintf("https://workbench.seedling.abc-cluster.cloud/%s/", user)
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
