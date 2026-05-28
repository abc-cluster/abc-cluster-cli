package workbench

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// hubURL returns the JupyterHub URL for this context.
// Falls back to the seedling default if not configured.
func hubURL(ctx config.Context) string {
	if u, ok := config.GetAdminFloorField(&ctx.Admin.Services, "workbench", "hub_url"); ok && u != "" {
		return u
	}
	return "https://workbench.seedling.abc-cluster.cloud"
}

// hubControlPanelURL returns the hub's server-management page.
func hubControlPanelURL(ctx config.Context) string {
	return hubURL(ctx) + "/hub/home"
}

// resolveUser extracts the current username from the active context.
// Returns empty string if no user is set.
func resolveUser(ctx config.Context) string {
	if u := strings.TrimSpace(ctx.Admin.Whoami); u != "" {
		return u
	}
	if ctx.Auth != nil {
		return strings.TrimSpace(ctx.Auth.Whoami)
	}
	return ""
}

// printHubStart prints the JupyterHub workbench URL and instructions,
// then tries to open the browser. Used by 'abc workbench start'.
func printHubStart(cmd *cobra.Command, ctx config.Context, user string) {
	url := hubURL(ctx)
	out := cmd.OutOrStdout()
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Workbench: %s\n", url)
	if user != "" {
		fmt.Fprintf(out, "  Username:  %s\n", user)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Log in and click \"Start My Server\" to launch JupyterLab.")
	fmt.Fprintln(out, "  Your home directory and installed packages persist across sessions.")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Stop your server: %s\n", hubControlPanelURL(ctx))
	fmt.Fprintln(out)
	openBrowser(url)
}

// printHubStatus prints hub status for users without an active Docker/VM session.
// Used by 'abc workbench status' and 'abc workbench stop' as a graceful fallback.
func printHubStatus(cmd *cobra.Command, ctx config.Context, verb string) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  Workbench: %s\n", hubURL(ctx))
	fmt.Fprintf(out, "  Manage:    %s\n", hubControlPanelURL(ctx))
	if verb != "" {
		fmt.Fprintf(out, "\n  To %s your server, visit the Manage URL above.\n", verb)
	}
}

// openBrowser tries to open url in the default browser. Best-effort; never errors.
func openBrowser(url string) {
	var bin string
	switch runtime.GOOS {
	case "darwin":
		bin = "open"
	case "linux":
		bin = "xdg-open"
	default:
		return
	}
	_ = exec.Command(bin, url).Start()
}
