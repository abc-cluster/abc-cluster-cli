package node

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// InstallTailscale installs Tailscale on the target (Linux only — macOS/Windows
// assume the GUI app is present) and runs `tailscale up` to join the tailnet.
//
// Patterns borrowed from tscli (Tailscale API patterns) and hashi-up (install flow).
func InstallTailscale(ctx context.Context, ex Executor, key, hostname string, w io.Writer) error {
	fmt.Fprintf(w, "\n  Installing Tailscale...\n")

	// Linux: run the official install script (curl | sh)
	if ex.OS() == "linux" {
		fmt.Fprintf(w, "    Running Tailscale install script...\n")
		script := "curl -fsSL https://tailscale.com/install.sh | sudo sh"
		if err := ex.Run(ctx, script, LineWriter(w, "      ")); err != nil {
			return fmt.Errorf("tailscale install script: %w", err)
		}
	}

	// Build `tailscale up` command — common for all platforms
	args := []string{"tailscale up", "--auth-key=" + key}
	if hostname != "" {
		args = append(args, "--hostname="+hostname)
	}
	upCmd := strings.Join(args, " ")

	// On Linux/macOS the binary may need sudo (tailscaled must be running)
	if ex.OS() != "windows" {
		combined := fmt.Sprintf("sudo %s 2>&1 || %s 2>&1", upCmd, upCmd)
		if err := ex.Run(ctx, combined, LineWriter(w, "      ")); err != nil {
			return fmt.Errorf("tailscale up: %w", err)
		}
	} else {
		if err := ex.Run(ctx, upCmd, LineWriter(w, "      ")); err != nil {
			return fmt.Errorf("tailscale up: %w", err)
		}
	}

	// Confirm tailnet IP (informational)
	var buf strings.Builder
	_ = ex.Run(ctx, "tailscale ip -4 2>/dev/null || tailscale ip 2>/dev/null | head -1", &buf)
	tsIP := strings.TrimSpace(buf.String())
	if tsIP != "" {
		fmt.Fprintf(w, "    ✓ Joined tailnet (Tailscale IP: %s)\n", tsIP)
	} else {
		fmt.Fprintf(w, "    ✓ tailscale up completed\n")
	}

	return nil
}
