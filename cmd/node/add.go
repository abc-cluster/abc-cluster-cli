package node

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a compute node to the cluster",
		Long: `Add a compute node to the ABC cluster.

Three modes (mutually exclusive):

  --cloud      Provision a new VM via the cloud gateway (requires --cloud flag)
  --host=<ip>  SSH into a remote server and install Nomad there
  --local      Install Nomad on the current machine

Tailscale is off by default (direct-join mode). Add --tailscale and
--tailscale-auth-key=<key> to enrol the node into a Tailscale tailnet.

Examples:
  # Cloud-provisioned VM
  abc node add --cloud --cluster=za-cpt --type=n2-standard-8 --count=2

  # Remote Linux server via SSH (direct-join, no Tailscale)
  abc node add --host=192.168.1.50 --user=ubuntu \
    --server-join=10.0.0.1 --datacenter=za-cpt

  # Remote Linux server via SSH (with Tailscale)
  abc node add --host=192.168.1.50 --user=ubuntu \
    --tailscale --tailscale-auth-key=tskey-auth-... \
    --server-join=100.64.0.1 --datacenter=za-cpt

  # Local machine (direct-join)
  abc node add --local \
    --server-join=10.0.0.5 --node-class=workstation`,
		RunE: runNodeAdd,
	}

	// ── Cloud flags (existing behaviour) ──────────────────────────────────────
	cmd.Flags().String("cluster", "", "Target cluster name (or set --cluster / ABC_CLUSTER)")
	cmd.Flags().String("type", "", "VM instance type (e.g. n2-standard-8, g4dn.xlarge)")
	cmd.Flags().Int("count", 1, "Number of nodes to provision")

	// ── Transport flags (new) ─────────────────────────────────────────────────
	cmd.Flags().Bool("local", false, "Install on the current machine")
	cmd.Flags().String("host", "", "SSH target host or IP for remote installation")
	cmd.Flags().String("user", "", "SSH user for remote install (default: current OS user)")
	cmd.Flags().String("ssh-key", "", "Path to SSH private key (default: ~/.ssh/id_rsa, then SSH agent)")
	cmd.Flags().Int("ssh-port", 22, "SSH port (default: 22)")

	// ── Nomad — node role ─────────────────────────────────────────────────────
	cmd.Flags().Bool("server", false, "Also enable Nomad server mode (advanced)")

	// ── Nomad — cluster join ──────────────────────────────────────────────────
	cmd.Flags().String("nomad-version", "", "Nomad version to install (default: latest stable)")
	cmd.Flags().String("datacenter", "default", "Nomad datacenter label")
	cmd.Flags().String("node-class", "", "Nomad node class label (optional)")
	cmd.Flags().StringArray("server-join", nil, "Nomad server address(es) to join (repeatable); maps to server_join.retry_join")
	cmd.Flags().String("encrypt", "", "Nomad gossip encryption key")
	cmd.Flags().Bool("acl", false, "Enable Nomad ACL system on this node")

	// ── Nomad — network bind ──────────────────────────────────────────────────
	cmd.Flags().String("address", "", "Address the agent binds to (default: 0.0.0.0)")
	cmd.Flags().String("advertise", "", "Address the agent advertises externally (useful behind NAT)")

	// ── Nomad — TLS ───────────────────────────────────────────────────────────
	cmd.Flags().String("ca-file", "", "CA certificate file path")
	cmd.Flags().String("cert-file", "", "Agent certificate file path")
	cmd.Flags().String("key-file", "", "Agent certificate key file path")

	// ── Nomad — service control ───────────────────────────────────────────────
	cmd.Flags().Bool("skip-enable", false, "Install binary and config but do not enable the service")
	cmd.Flags().Bool("skip-start", false, "Enable service but do not start it immediately")

	// ── Tailscale ────────────────────────────────────────────────────────────
	cmd.Flags().Bool("tailscale", false, "Join a Tailscale tailnet during provisioning (default: false — direct-join mode)")
	cmd.Flags().String("tailscale-auth-key", "", "Tailscale pre-auth key (required when --tailscale is set)")
	cmd.Flags().String("tailscale-hostname", "", "Override Tailscale hostname (default: OS hostname)")

	// ── Other ────────────────────────────────────────────────────────────────
	cmd.Flags().Bool("dry-run", false, "Print what would be executed without making changes")
	cmd.Flags().Bool("skip-preflight", false, "Skip OS compatibility checks")

	// ── Script generation ────────────────────────────────────────────────────
	cmd.Flags().Bool("print-commands", false, "Print a self-contained shell script covering all install steps (no execution)")
	cmd.Flags().String("target-os", "", "Target OS/arch for --print-commands with --host (e.g. linux/amd64, darwin/arm64; default: linux/amd64)")

	return cmd
}

func runNodeAdd(cmd *cobra.Command, _ []string) error {
	// --print-commands: emit a shell script and exit without connecting anywhere
	if printCmds, _ := cmd.Flags().GetBool("print-commands"); printCmds {
		return runPrintCommands(cmd)
	}

	isCloud := utils.CloudFromCmd(cmd)
	isLocal, _ := cmd.Flags().GetBool("local")
	host, _ := cmd.Flags().GetString("host")

	// Route to the correct mode
	switch {
	case isCloud:
		return runCloudAdd(cmd)
	case host != "":
		return runSSHAdd(cmd, host)
	case isLocal:
		return runLocalAdd(cmd)
	default:
		return fmt.Errorf("specify a transport: --cloud, --host=<ip>, or --local")
	}
}

// runPrintCommands emits a complete, self-contained shell script to stdout
// covering every install step (Tailscale, Nomad, config, service, health check).
// No SSH connection or local execution is performed.
func runPrintCommands(cmd *cobra.Command) error {
	isCloud := utils.CloudFromCmd(cmd)
	if isCloud {
		return fmt.Errorf("--print-commands is not supported with --cloud (provisioning is handled server-side)")
	}

	isLocal, _ := cmd.Flags().GetBool("local")
	targetOSFlag, _ := cmd.Flags().GetString("target-os")

	var goos, goarch string
	if isLocal {
		goos, goarch = localOSArch()
	} else {
		var err error
		goos, goarch, err = parseTargetOS(targetOSFlag)
		if err != nil {
			return err
		}
	}

	// Assemble config from flags (same as runInstall)
	serverJoin, _ := cmd.Flags().GetStringArray("server-join")
	nodeCfg := NodeConfig{
		Datacenter: mustGetString(cmd, "datacenter"),
		NodeClass:  mustGetString(cmd, "node-class"),
		ServerJoin: serverJoin,
		Encrypt:    mustGetString(cmd, "encrypt"),
		Address:    mustGetString(cmd, "address"),
		Advertise:  mustGetString(cmd, "advertise"),
		CAFile:     mustGetString(cmd, "ca-file"),
		CertFile:   mustGetString(cmd, "cert-file"),
		KeyFile:    mustGetString(cmd, "key-file"),
	}
	nodeCfg.ACL, _ = cmd.Flags().GetBool("acl")
	nodeCfg.ServerMode, _ = cmd.Flags().GetBool("server")

	nomadVersion, _ := cmd.Flags().GetString("nomad-version")
	skipEnable, _ := cmd.Flags().GetBool("skip-enable")
	skipStart, _ := cmd.Flags().GetBool("skip-start")
	useTailscale, _ := cmd.Flags().GetBool("tailscale")
	tsAuthKey, _ := cmd.Flags().GetString("tailscale-auth-key")
	tsHostname, _ := cmd.Flags().GetString("tailscale-hostname")

	cfg := NomadInstallConfig{
		Version:    nomadVersion,
		NodeConfig: nodeCfg,
		SkipEnable: skipEnable,
		SkipStart:  skipStart,
	}

	return printSetupScript(cmd.OutOrStdout(), goos, goarch, cfg, useTailscale, tsAuthKey, tsHostname, skipEnable, skipStart)
}

// ─── Cloud path (unchanged from original) ────────────────────────────────────

func runCloudAdd(cmd *cobra.Command) error {
	nc := nomadClientFromCmd(cmd)

	cluster := utils.ClusterFromCmd(cmd)
	if v, _ := cmd.Flags().GetString("cluster"); v != "" {
		cluster = v
	}
	nodeType, _ := cmd.Flags().GetString("type")
	datacenter, _ := cmd.Flags().GetString("datacenter")
	count, _ := cmd.Flags().GetInt("count")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	req := map[string]interface{}{
		"Cluster":    cluster,
		"NodeType":   nodeType,
		"Datacenter": datacenter,
		"Count":      count,
		"DryRun":     dryRun,
	}

	var resp map[string]interface{}
	if err := nc.CloudAddNode(cmd.Context(), req, &resp); err != nil {
		return fmt.Errorf("provisioning node: %w", err)
	}

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "  Dry-run: %d %s node(s) would be added to cluster %q.\n",
			count, nodeType, cluster)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  Node provisioning started (%d x %s).\n", count, nodeType)
	return nil
}

// ─── Local path ───────────────────────────────────────────────────────────────

func runLocalAdd(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	ex := newLocalExec()
	fmt.Fprintf(out, "\n  Installing on local machine (%s/%s)...\n", ex.OS(), ex.Arch())
	return runInstall(cmd.Context(), cmd, ex, out)
}

// ─── SSH path ─────────────────────────────────────────────────────────────────

func runSSHAdd(cmd *cobra.Command, host string) error {
	out := cmd.OutOrStdout()

	user, _ := cmd.Flags().GetString("user")
	if user == "" {
		user = os.Getenv("USER")
		if user == "" {
			user = "root"
		}
	}
	port, _ := cmd.Flags().GetInt("ssh-port")
	keyFile, _ := cmd.Flags().GetString("ssh-key")

	fmt.Fprintf(out, "\n  Connecting to %s@%s:%d...\n", user, host, port)

	ex, err := newSSHExec(SSHConfig{
		Host:    host,
		Port:    port,
		User:    user,
		KeyFile: keyFile,
	})
	if err != nil {
		return fmt.Errorf("SSH connect: %w", err)
	}
	defer ex.Close()
	fmt.Fprintf(out, "  ✓ Connected (%s/%s)\n", ex.OS(), ex.Arch())

	return runInstall(cmd.Context(), cmd, ex, out)
}

// ─── Shared install orchestration ─────────────────────────────────────────────

func runInstall(ctx context.Context, cmd *cobra.Command, ex Executor, w io.Writer) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	skipPreflight, _ := cmd.Flags().GetBool("skip-preflight")
	useTailscale, _ := cmd.Flags().GetBool("tailscale")
	tsAuthKey, _ := cmd.Flags().GetString("tailscale-auth-key")
	tsHostname, _ := cmd.Flags().GetString("tailscale-hostname")

	// Validate: auth key required only when --tailscale is explicitly requested
	if useTailscale && tsAuthKey == "" {
		return fmt.Errorf("--tailscale-auth-key is required when --tailscale is set\n" +
			"  Obtain a pre-auth key from https://login.tailscale.com/admin/settings/keys\n" +
			"  Or omit --tailscale to install without Tailscale (direct-join mode)")
	}

	// Collect Nomad config
	serverJoin, _ := cmd.Flags().GetStringArray("server-join")
	nodeCfg := NodeConfig{
		Datacenter: mustGetString(cmd, "datacenter"),
		NodeClass:  mustGetString(cmd, "node-class"),
		ServerJoin: serverJoin,
		Encrypt:    mustGetString(cmd, "encrypt"),
		Address:    mustGetString(cmd, "address"),
		Advertise:  mustGetString(cmd, "advertise"),
		CAFile:     mustGetString(cmd, "ca-file"),
		CertFile:   mustGetString(cmd, "cert-file"),
		KeyFile:    mustGetString(cmd, "key-file"),
	}
	nodeCfg.ACL, _ = cmd.Flags().GetBool("acl")
	nodeCfg.ServerMode, _ = cmd.Flags().GetBool("server")

	nomadVersion, _ := cmd.Flags().GetString("nomad-version")
	skipEnable, _ := cmd.Flags().GetBool("skip-enable")
	skipStart, _ := cmd.Flags().GetBool("skip-start")

	if dryRun {
		printDryRun(w, ex, nodeCfg, nomadVersion, useTailscale, tsHostname, serverJoin)
		return nil
	}

	// 1. Preflight checks
	var pf *PreflightResult
	if !skipPreflight {
		var err error
		pf, err = RunPreflight(ctx, ex, w)
		if err != nil {
			return err
		}
	} else {
		pf = &PreflightResult{OS: ex.OS(), Arch: ex.Arch(), HasSudo: true, InitSystem: initSystemFor(ex.OS())}
	}

	// 2. Tailscale (only when --tailscale flag is set)
	if useTailscale && !pf.TailscaleConnected {
		if pf.TailscaleInstalled {
			// Already installed — just run tailscale up
			fmt.Fprintf(w, "\n  Joining tailnet...\n")
			args := "tailscale up --auth-key=" + tsAuthKey
			if tsHostname != "" {
				args += " --hostname=" + tsHostname
			}
			if err := ex.Run(ctx, "sudo "+args+" 2>&1 || "+args+" 2>&1", LineWriter(w, "    ")); err != nil {
				return fmt.Errorf("tailscale up: %w", err)
			}
		} else {
			if err := InstallTailscale(ctx, ex, tsAuthKey, tsHostname, w); err != nil {
				return err
			}
		}
	}

	// 3. Nomad
	if !pf.NomadInstalled {
		installCfg := NomadInstallConfig{
			Version:    nomadVersion,
			NodeConfig: nodeCfg,
			SkipEnable: skipEnable,
			SkipStart:  skipStart,
		}
		if err := InstallNomad(ctx, ex, installCfg, w); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "\n  Nomad already installed — skipping install.\n")
		fmt.Fprintf(w, "  Note: Run 'abc node add --local --skip-preflight' to force reinstall.\n")
	}

	// 4. Verify: poll local Nomad agent
	if !skipStart {
		fmt.Fprintf(w, "\n  Verifying...\n")
		if err := waitForNomadAgent(ctx, ex, w); err != nil {
			fmt.Fprintf(w, "    ! Could not verify Nomad agent: %v\n", err)
			fmt.Fprintf(w, "    Check: sudo journalctl -u nomad -n 50\n")
		}
	}

	fmt.Fprintf(w, "\n  Done. Run 'abc node list --sudo' to see the new node.\n")
	return nil
}

// waitForNomadAgent polls http://127.0.0.1:4646/v1/agent/self until Nomad responds.
// For remote nodes this checks via the SSH executor running curl on the remote host.
func waitForNomadAgent(ctx context.Context, ex Executor, w io.Writer) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		// Check via executor: run curl on the target
		var buf strings.Builder
		err := ex.Run(ctx, "curl -sf http://127.0.0.1:4646/v1/agent/self 2>/dev/null | head -1", &buf)
		if err == nil && strings.TrimSpace(buf.String()) != "" {
			fmt.Fprintf(w, "    ✓ Nomad agent is healthy\n")
			return nil
		}
		// Fallback for local: try direct HTTP
		if _, ok := ex.(*localExec); ok {
			resp, herr := http.Get("http://127.0.0.1:4646/v1/agent/self") //nolint:noctx
			if herr == nil && resp.StatusCode == 200 {
				resp.Body.Close()
				fmt.Fprintf(w, "    ✓ Nomad agent is healthy\n")
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("Nomad agent did not respond within 60s")
}

// ─── Dry-run ──────────────────────────────────────────────────────────────────

func printDryRun(w io.Writer, ex Executor, cfg NodeConfig, version string, useTailscale bool, tsHostname string, serverJoin []string) {
	fmt.Fprintf(w, "\n  Dry-run plan:\n")
	fmt.Fprintf(w, "    Target:       %s/%s\n", ex.OS(), ex.Arch())
	fmt.Fprintf(w, "    Datacenter:   %s\n", cfg.Datacenter)
	if cfg.NodeClass != "" {
		fmt.Fprintf(w, "    Node class:   %s\n", cfg.NodeClass)
	}
	if len(serverJoin) > 0 {
		fmt.Fprintf(w, "    Server join:  %s\n", strings.Join(serverJoin, ", "))
	}
	if useTailscale {
		fmt.Fprintf(w, "    Tailscale:    install + tailscale up")
		if tsHostname != "" {
			fmt.Fprintf(w, " --hostname=%s", tsHostname)
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintf(w, "    Tailscale:    off (direct-join mode)\n")
	}
	if version == "" {
		version = "latest"
	}
	fmt.Fprintf(w, "    Nomad:        install %s\n", version)
	binPath, _, cfgPath, _ := nomadPaths(ex.OS())
	fmt.Fprintf(w, "    Binary path:  %s\n", binPath)
	fmt.Fprintf(w, "    Config path:  %s\n", cfgPath)
	fmt.Fprintf(w, "\n  (no changes made — remove --dry-run to execute)\n")
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func mustGetString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func initSystemFor(goos string) string {
	switch goos {
	case "darwin":
		return "launchd"
	case "windows":
		return "none"
	default:
		return "systemd"
	}
}
