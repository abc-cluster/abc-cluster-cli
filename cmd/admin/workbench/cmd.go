// Package workbench implements the "abc admin services workbench" command group.
//
// These are operator-runnable commands — they require hub admin credentials and
// SSH access to the platform node. They are distinct from the user-facing
// "abc workbench" commands.
//
// Usage:
//
//	abc admin services workbench sessions                  List active JupyterHub sessions
//	abc admin services workbench watch                     Idle-session reaper (polls + stops + imports)
//	abc admin services workbench import-history <slot>     Import atuin shell history for one slot
package workbench

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	wbinternal "github.com/abc-cluster/abc-cluster-cli/internal/workbench"
	"github.com/spf13/cobra"
)

// NewCmd returns the "workbench" subcommand group under abc admin services.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workbench",
		Short: "Operator commands for the JupyterHub workbench service",
		Long: `Operator commands for the abc-workbench JupyterHub service.

Requires admin.services.workbench.hub_token in your abc config context.
Set it with:

  abc config set contexts.<name>.admin.services.workbench.hub_token <token>

Available subcommands:

  sessions           List active sessions (running JupyterHub servers + idle time)
  watch              Idle-session reaper — polls, imports atuin history, stops idle servers
  import-history     Import atuin shell history for one slot from the platform node`,
	}

	cmd.AddCommand(newSessionsCmd())
	cmd.AddCommand(newWatchCmd())
	cmd.AddCommand(newImportHistoryCmd())
	return cmd
}

// hubClientFromCtx builds a HubClient from the active config context.
// Returns an error if hub_token is not configured.
func hubClientFromCtx(actx config.Context) (*wbinternal.HubClient, error) {
	tok, ok := wbinternal.HubTokenFromCtx(actx)
	if !ok {
		return nil, fmt.Errorf(
			"hub_token not configured\n" +
				"  Run: abc config set contexts.<name>.admin.services.workbench.hub_token <token>\n" +
				"  Generate a token on the server: tljh-config generate-admin-token",
		)
	}
	hubURL := wbinternal.HubURLFromCtx(actx)
	return wbinternal.NewHubClient(hubURL, tok), nil
}

// nodeFromCtx resolves the SSH alias for the platform node (e.g. "sun-aither").
// Falls back to "aither" if not configured.
func nodeFromCtx(actx config.Context, override string) wbinternal.NodeSSH {
	if override != "" {
		return wbinternal.NodeSSH{Host: override}
	}
	if wn, ok := config.GetAdminFloorField(&actx.Admin.Services, "workbench", "node"); ok && wn != "" {
		return wbinternal.NodeSSHFromName(wn)
	}
	return wbinternal.NodeSSHFromName("aither")
}
