package compute

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// linuxNodeDebugScriptBody returns a shell script for the target (local or SSH).
// Uses sudo for root-only paths; sshExec rewrites sudo when --password / ABC_NODE_PASSWORD is set.
func linuxNodeDebugScriptBody() string {
	dir := cniPluginsInstallDir
	return fmt.Sprintf(`set +e
echo "=== Linux bridge kernel module (Nomad fingerprint for network mode=bridge) ==="
if [ -d /sys/module/bridge ]; then
  echo "bridge.sysfs: present (module loaded)"
else
  echo "bridge.sysfs: MISSING — Nomad disables bridge scheduling; jobs fail Constraint \"missing network\""
fi
if grep -qE '^bridge[[:space:]]' /proc/modules 2>/dev/null; then
  echo "bridge.proc_modules: listed"
else
  echo "bridge.proc_modules: not listed"
fi
if [ -f /etc/modules-load.d/nomad-bridge.conf ]; then
  echo "bridge.autoload: /etc/modules-load.d/nomad-bridge.conf"
  cat /etc/modules-load.d/nomad-bridge.conf
fi

echo ""
echo "=== Nomad client config (network-related lines) ==="
if sudo test -r /etc/nomad.d/client.hcl 2>/dev/null; then
  sudo grep -E '^[[:space:]]*(cni_path|network_interface|client[[:space:]]*\{|addresses|plugin[[:space:]]*"containerd-driver)' /etc/nomad.d/client.hcl 2>/dev/null || sudo cat /etc/nomad.d/client.hcl
else
  echo "(cannot read /etc/nomad.d/client.hcl — check sudo / file exists)"
fi

echo ""
echo "=== CNI plugin directory (%s) ==="
CNI_DIR=%q
if [ -d "$CNI_DIR" ]; then
  for p in loopback bridge portmap host-local firewall; do
    if [ -x "$CNI_DIR/$p" ]; then echo "cni.$p: executable OK"; else echo "cni.$p: missing or not executable"; fi
  done
  extras=0
  for junk in LICENSE README.md README.md.bak; do
    if [ -f "$CNI_DIR/$junk" ]; then echo "cni.warn: non-plugin file $CNI_DIR/$junk (Nomad logs unexpected non-executable)"; extras=$((extras+1)); fi
  done
  if [ "$extras" -eq 0 ]; then echo "cni.junk_files: none of LICENSE/README in plugin dir"; fi
else
  echo "cni_dir: MISSING — set client cni_path and install reference plugins"
fi

echo ""
echo "=== Nomad service + containerd driver binary ==="
if command -v systemctl >/dev/null 2>&1; then
  printf "nomad.service: "; systemctl is-active nomad 2>/dev/null || echo "unknown"
  printf "containerd.service: "; systemctl is-active containerd 2>/dev/null || echo "n/a"
else
  echo "systemctl: not available"
fi
if [ -x /opt/nomad/plugins/containerd-driver ]; then echo "containerd-driver: present"; else echo "containerd-driver: absent under /opt/nomad/plugins"; fi
command -v nomad >/dev/null 2>&1 && nomad version 2>/dev/null | head -1 || true

echo ""
echo "=== Kernel / forwarding (container egress) ==="
sysctl net.ipv4.ip_forward 2>/dev/null || true

echo ""
echo "=== Recent Nomad logs (bridge / CNI / network fingerprint) ==="
if command -v journalctl >/dev/null 2>&1; then
  sudo journalctl -u nomad -n 200 --no-pager 2>/dev/null | grep -iE 'bridge kernel module|bridge network mode disabled|cni_path|unexpected non-executable|network_interface|missing network|fingerprint_mgr.*network' | tail -n 40 || echo "(no matching log lines in last 200 entries)"
else
  echo "journalctl: not available"
fi
`, dir, dir)
}

func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Per-node operator diagnostics (SSH or local shell)",
	}
	cmd.AddCommand(newNodeDebugCmd())
	return cmd
}

func newNodeDebugCmd() *cobra.Command {
	long := fmt.Sprintf(strings.TrimSpace(`
Collect read-only checks that explain common Nomad placement failures for jobs
using network { mode = "bridge" } (for example abc-nodes service specs).

What this checks
  • Linux "bridge" kernel module — required for Nomad's bridge fingerprinter.
    Without it the scheduler reports Constraint "missing network" even when CNI
    binaries exist.
  • client.hcl lines for cni_path and network_interface (avoid bandwidth-only
    virtual NICs like tailscale0 for network_interface).
  • Reference CNI binaries under %s and obvious tarball junk.
  • Snippets from journalctl -u nomad for bridge/CNI fingerprint messages.

SSH flags match "abc infra compute add --remote=…" (see also ABC_NODE_PASSWORD for sudo).

Examples:
  abc infra compute node debug --remote=sun-aither
  abc infra compute node debug --remote=10.0.0.5 --user=ubuntu --password="$ABC_NODE_PASSWORD"
  abc infra compute node debug   # same checks on the local machine (Linux only)
`), cniPluginsInstallDir)

	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Print Linux Nomad client networking diagnostics (bridge/CNI)",
		Long:  long,
		RunE:  runNodeDebug,
	}
	registerComputeSSHTransportFlags(cmd)
	return cmd
}

func runNodeDebug(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	remote, _ := cmd.Flags().GetString("remote")

	var ex Executor
	if remote == "" {
		ex = newLocalExec(ctx)
		fmt.Fprintf(out, "\n  Local diagnostics (%s/%s)...\n\n", ex.OS(), ex.Arch())
	} else {
		sex, err := sshExecutorFromRemoteFlags(ctx, cmd, remote, out)
		if err != nil {
			return fmt.Errorf("SSH connect: %w", err)
		}
		defer sex.Close()
		ex = sex
		fmt.Fprintf(out, "  ✓ Connected (%s/%s)\n\n", ex.OS(), ex.Arch())
	}

	if ex.OS() != "linux" {
		fmt.Fprintf(out, "These checks target Linux Nomad clients (bridge/CNI). Current OS: %s\n", ex.OS())
		printNodeDebugHints(out)
		return nil
	}

	script := linuxNodeDebugScriptBody()
	if err := ex.Run(ctx, "/bin/sh -c "+shellQuote(script), out); err != nil {
		fmt.Fprintf(out, "\n(warning: diagnostic shell exited with an error: %v)\n", err)
	}
	fmt.Fprintln(out)
	printNodeDebugHints(out)
	return nil
}

func printNodeDebugHints(w io.Writer) {
	fmt.Fprintln(w, "--- Hints ---")
	fmt.Fprintln(w, "• Constraint \"missing network\" on bridge jobs: sudo modprobe bridge; persist with")
	fmt.Fprintln(w, "    echo bridge | sudo tee /etc/modules-load.d/nomad-bridge.conf")
	fmt.Fprintln(w, "  then sudo systemctl restart nomad. (abc infra compute add with CNI install configures this.)")
	fmt.Fprintln(w, "• CNI: ensure reference plugins under client cni_path; remove LICENSE/README from that directory if present.")
	fmt.Fprintln(w, "• network_interface: omit or set to a physical NIC; do not use tailscale0 for Nomad bandwidth fingerprinting.")
	fmt.Fprintln(w, "• After fixes on a remote node, restart nomad then re-run the job or wait for the node catalog to update.")
}
