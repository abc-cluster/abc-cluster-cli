package node

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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
either provide --tailscale-auth-key=<key> or let abc create one using
TAILSCALE_API_KEY.

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

  # Remote Linux server via SSH (with Tailscale, auth key auto-created from TAILSCALE_API_KEY)
  abc node add --host=192.168.1.50 --user=ubuntu \
    --tailscale --tailscale-key-ephemeral --tailscale-key-expiry=2h \
    --server-join=100.64.0.1 --datacenter=za-cpt

  # Local machine (direct-join)
  abc node add --local \
    --server-join=10.0.0.5 --node-class=workstation

  # Remote server via SSH jump/bastion host
  abc node add --host=10.10.0.50 --user=ubuntu \
    --jump-host=bastion.example.com --jump-user=ec2-user \
    --server-join=10.10.0.1 --datacenter=za-cpt

  # Print a self-contained install script (no SSH connection made)
  abc node add --host=10.10.0.50 --print-commands \
    --target-os=linux/amd64 --server-join=10.10.0.1`,
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
	cmd.Flags().Bool("skip-host-key-check", false, "Disable known_hosts verification (insecure; for dev/testing only)")
	cmd.Flags().String("password", "", "Node login password (used for SSH auth and sudo -S; also ABC_NODE_PASSWORD env var)")

	// ── Jump host flags ───────────────────────────────────────────────────────
	cmd.Flags().String("jump-host", "", "SSH jump/bastion host to proxy through (equivalent to ssh -J)")
	cmd.Flags().String("jump-user", "", "Username on the jump host (default: same as --user)")
	cmd.Flags().Int("jump-port", 22, "SSH port on the jump host (default: 22)")
	cmd.Flags().String("jump-key", "", "SSH private key for the jump host (default: same as --ssh-key)")

	// ── Nomad — node role ─────────────────────────────────────────────────────
	// NOTE: Intentionally disabled for now; abc node add is client-only.
	// cmd.Flags().Bool("server", false, "Also enable Nomad server mode (advanced)")

	// ── Nomad — cluster join ──────────────────────────────────────────────────
	cmd.Flags().String("nomad-version", "", "Nomad version to install (default: latest stable)")
	cmd.Flags().String("datacenter", "default", "Nomad datacenter label")
	cmd.Flags().String("node-class", "", "Nomad node class label (optional)")
	cmd.Flags().StringArray("server-join", nil, "Nomad server address(es) to join (repeatable); maps to client.server_join.retry_join")
	cmd.Flags().String("network-interface", "", "Nomad client network_interface value (defaults to tailscale0 when using Tailscale)")
	cmd.Flags().StringArray("host-volume", nil, "Nomad client host volume in name=path[:read_only] format (repeatable)")
	cmd.Flags().Bool("scratch-host-volume", true, "Configure a default Nomad client host volume named scratch")
	cmd.Flags().String("scratch-host-volume-path", "/opt/nomad/scratch", "Path for the default scratch host volume")
	cmd.Flags().StringArray("community-driver", nil, "Experimental: install community task drivers (currently supported: containerd)")
	cmd.Flags().String("containerd-nerdctl-version", defaultContainerdNerdctlVersion, "Experimental: nerdctl-full version for --community-driver=containerd")
	cmd.Flags().String("containerd-driver-version", defaultContainerdDriverVersion, "Experimental: nomad-driver-containerd release version for --community-driver=containerd")
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
	cmd.Flags().String("tailscale-auth-key", "", "Tailscale pre-auth key (optional when --tailscale is set; auto-created if omitted and TAILSCALE_API_KEY is set)")
	cmd.Flags().String("tailscale-hostname", "", "Override Tailscale hostname (default: OS hostname)")
	cmd.Flags().Bool("tailscale-create-auth-key", true, "Auto-create a Tailscale auth key via API when --tailscale-auth-key is omitted (requires TAILSCALE_API_KEY)")
	cmd.Flags().Bool("tailscale-key-ephemeral", true, "When auto-creating a Tailscale auth key, register the node as ephemeral")
	cmd.Flags().Bool("tailscale-key-reusable", false, "When auto-creating a Tailscale auth key, make it reusable")
	cmd.Flags().Duration("tailscale-key-expiry", 24*time.Hour, "When auto-creating a Tailscale auth key, set key expiry (for example 30m, 2h, 24h)")
	cmd.Flags().Bool("tailscale-key-preauthorized", true, "When auto-creating a Tailscale auth key, mark devices as preauthorized")
	cmd.Flags().String("tailscale-key-description", "", "When auto-creating a Tailscale auth key, set key description")
	cmd.Flags().Bool("nomad-use-tailscale-ip", false, "Set Nomad advertise address to the node's Tailscale IPv4; also works when Tailscale was configured manually")
	cmd.Flags().String("package-install-method", packageInstallMethodStatic, "Install method for Nomad and Tailscale: static (default) or package-manager")
	cmd.Flags().String("tailscale-install-method", "", "DEPRECATED: use --package-install-method")
	_ = cmd.Flags().MarkHidden("tailscale-install-method")

	// ── Other ────────────────────────────────────────────────────────────────
	cmd.Flags().Bool("dry-run", false, "Print what would be executed without making changes")
	cmd.Flags().Bool("skip-preflight", false, "Skip OS compatibility checks")

	// ── Script generation ────────────────────────────────────────────────────
	cmd.Flags().Bool("print-commands", false, "Print a self-contained shell script covering all install steps (no execution)")
	cmd.Flags().String("target-os", "", "Target OS/arch for --print-commands with --host (e.g. linux/amd64, darwin/arm64; default: linux/amd64)")

	return cmd
}

func shouldRefreshNomadConfig(cmd *cobra.Command, nomadUseTailscaleIP bool) bool {
	if nomadUseTailscaleIP {
		return true
	}
	configFlags := []string{
		"datacenter",
		"node-class",
		"server-join",
		"network-interface",
		"host-volume",
		"scratch-host-volume",
		"scratch-host-volume-path",
		"encrypt",
		"acl",
		"address",
		"advertise",
		"ca-file",
		"cert-file",
		"key-file",
	}
	for _, name := range configFlags {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
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
	nomadVersion, _ := cmd.Flags().GetString("nomad-version")
	skipEnable, _ := cmd.Flags().GetBool("skip-enable")
	skipStart, _ := cmd.Flags().GetBool("skip-start")
	useTailscale, _ := cmd.Flags().GetBool("tailscale")
	tsAuthKey, _ := cmd.Flags().GetString("tailscale-auth-key")
	tsHostname, _ := cmd.Flags().GetString("tailscale-hostname")
	tsCreateAuthKey, _ := cmd.Flags().GetBool("tailscale-create-auth-key")
	tsKeyEphemeral, _ := cmd.Flags().GetBool("tailscale-key-ephemeral")
	tsKeyReusable, _ := cmd.Flags().GetBool("tailscale-key-reusable")
	tsKeyExpiry, _ := cmd.Flags().GetDuration("tailscale-key-expiry")
	nomadUseTailscaleIP, _ := cmd.Flags().GetBool("nomad-use-tailscale-ip")
	if useTailscale {
		nomadUseTailscaleIP = true
	}
	if tsKeyExpiry < 0 {
		return fmt.Errorf("--tailscale-key-expiry must be >= 0")
	}
	networkInterface := mustGetString(cmd, "network-interface")
	if !cmd.Flags().Changed("network-interface") && networkInterface == "" && (useTailscale || nomadUseTailscaleIP) {
		networkInterface = "tailscale0"
	}
	hostVolumes, err := hostVolumesFromFlags(cmd)
	if err != nil {
		return err
	}
	communityDrivers, err := communityDriverInstallConfigFromFlags(cmd)
	if err != nil {
		return err
	}
	if err := ensureExperimentalFeatureEnabled(cmd, communityDrivers.Requested(), "community driver installation"); err != nil {
		return err
	}
	if err := validateCommunityDriverTarget(goos, communityDrivers); err != nil {
		return err
	}
	if communityDrivers.Requested() && skipStart {
		return fmt.Errorf("community driver setup runs after the node joins the cluster; remove --skip-start when using --community-driver")
	}

	serverJoin, _ := cmd.Flags().GetStringArray("server-join")
	nodeCfg := NodeConfig{
		Datacenter:       mustGetString(cmd, "datacenter"),
		NodeClass:        mustGetString(cmd, "node-class"),
		NetworkInterface: networkInterface,
		ServerJoin:       serverJoin,
		HostVolumes:      hostVolumes,
		Encrypt:          mustGetString(cmd, "encrypt"),
		Address:          mustGetString(cmd, "address"),
		Advertise:        mustGetString(cmd, "advertise"),
		CAFile:           mustGetString(cmd, "ca-file"),
		CertFile:         mustGetString(cmd, "cert-file"),
		KeyFile:          mustGetString(cmd, "key-file"),
	}
	nodeCfg.ACL, _ = cmd.Flags().GetBool("acl")
	if cmd.Flags().Changed("advertise") && nomadUseTailscaleIP {
		nomadUseTailscaleIP = false
	}
	autoNomadAdvertise := nomadUseTailscaleIP && nodeCfg.Advertise == ""
	if autoNomadAdvertise {
		nodeCfg.Advertise = "${NOMAD_ADVERTISE}"
	}

	packageInstallMethod, err := resolvePackageInstallMethodFlag(cmd)
	if err != nil {
		return err
	}

	cfg := NomadInstallConfig{
		Version:       nomadVersion,
		InstallMethod: packageInstallMethod,
		NodeConfig:    nodeCfg,
		SkipEnable:    skipEnable,
		SkipStart:     skipStart,
	}

	return printSetupScript(
		cmd.OutOrStdout(),
		goos,
		goarch,
		cfg,
		useTailscale,
		tsAuthKey,
		tsHostname,
		packageInstallMethod,
		tsCreateAuthKey,
		tsKeyEphemeral,
		tsKeyReusable,
		tsKeyExpiry,
		autoNomadAdvertise,
		communityDrivers,
		skipEnable,
		skipStart,
	)
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

	// 1. Load ~/.ssh/config defaults for this alias (Hostname, Port, User,
	//    IdentityFile, ProxyJump, StrictHostKeyChecking).
	sshCfg, isAlias := loadSSHConfigEntry(host)

	// 2. CLI flags override config-file values.
	//    cmd.Flags().Changed() is true only when the user explicitly passed the
	//    flag — cobra defaults do NOT set Changed, so port=22 in ~/.ssh/config
	//    is correctly preserved when --ssh-port is omitted.
	if cmd.Flags().Changed("user") {
		sshCfg.User, _ = cmd.Flags().GetString("user")
	}
	if cmd.Flags().Changed("ssh-port") {
		sshCfg.Port, _ = cmd.Flags().GetInt("ssh-port")
	}
	if cmd.Flags().Changed("ssh-key") {
		sshCfg.KeyFile, _ = cmd.Flags().GetString("ssh-key")
	}
	if cmd.Flags().Changed("jump-host") {
		sshCfg.JumpHost, _ = cmd.Flags().GetString("jump-host")
	}
	if cmd.Flags().Changed("jump-user") {
		sshCfg.JumpUser, _ = cmd.Flags().GetString("jump-user")
	}
	if cmd.Flags().Changed("jump-port") {
		sshCfg.JumpPort, _ = cmd.Flags().GetInt("jump-port")
	}
	if cmd.Flags().Changed("jump-key") {
		sshCfg.JumpKeyFile, _ = cmd.Flags().GetString("jump-key")
	}
	// Boolean: OR the flag value with the config-file value (security-conservative).
	if skip, _ := cmd.Flags().GetBool("skip-host-key-check"); skip {
		sshCfg.SkipHostKeyCheck = true
	}

	// Resolve password: flag takes precedence over environment variable.
	password, _ := cmd.Flags().GetString("password")
	if password == "" {
		password = os.Getenv("ABC_NODE_PASSWORD")
	}
	sshCfg.Password = password

	// 3. Print connection banner.
	switch {
	case sshCfg.JumpHost != "":
		fmt.Fprintf(out, "\n  Connecting to %s@%s:%d via jump host %s...\n",
			sshCfg.User, host, sshCfg.Port, sshCfg.JumpHost)
	case isAlias:
		fmt.Fprintf(out, "\n  Connecting to %s@%s:%d (resolved: %s:%d via ~/.ssh/config)...\n",
			sshCfg.User, host, sshCfg.Port, sshCfg.Host, sshCfg.Port)
	default:
		fmt.Fprintf(out, "\n  Connecting to %s@%s:%d...\n", sshCfg.User, host, sshCfg.Port)
	}

	// 4. Dial and run install.
	ex, err := newSSHExec(sshCfg)
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
	tsCreateAuthKey, _ := cmd.Flags().GetBool("tailscale-create-auth-key")
	tsKeyEphemeral, _ := cmd.Flags().GetBool("tailscale-key-ephemeral")
	tsKeyReusable, _ := cmd.Flags().GetBool("tailscale-key-reusable")
	tsKeyExpiry, _ := cmd.Flags().GetDuration("tailscale-key-expiry")
	tsKeyPreauthorized, _ := cmd.Flags().GetBool("tailscale-key-preauthorized")
	tsKeyDescription, _ := cmd.Flags().GetString("tailscale-key-description")
	nomadUseTailscaleIP, _ := cmd.Flags().GetBool("nomad-use-tailscale-ip")
	if useTailscale {
		nomadUseTailscaleIP = true
	}
	packageInstallMethod, err := resolvePackageInstallMethodFlag(cmd)
	if err != nil {
		return err
	}
	if tsKeyExpiry < 0 {
		return fmt.Errorf("--tailscale-key-expiry must be >= 0")
	}
	networkInterface := mustGetString(cmd, "network-interface")
	if !cmd.Flags().Changed("network-interface") && networkInterface == "" && (useTailscale || nomadUseTailscaleIP) {
		networkInterface = "tailscale0"
	}
	hostVolumes, err := hostVolumesFromFlags(cmd)
	if err != nil {
		return err
	}
	communityDrivers, err := communityDriverInstallConfigFromFlags(cmd)
	if err != nil {
		return err
	}
	if err := ensureExperimentalFeatureEnabled(cmd, communityDrivers.Requested(), "community driver installation"); err != nil {
		return err
	}
	if err := validateCommunityDriverTarget(ex.OS(), communityDrivers); err != nil {
		return err
	}
	nomadVersion, _ := cmd.Flags().GetString("nomad-version")
	skipEnable, _ := cmd.Flags().GetBool("skip-enable")
	skipStart, _ := cmd.Flags().GetBool("skip-start")
	if communityDrivers.Requested() && skipStart {
		return fmt.Errorf("community driver setup runs after the node joins the cluster; remove --skip-start when using --community-driver")
	}

	// Collect Nomad config
	serverJoin, _ := cmd.Flags().GetStringArray("server-join")
	nodeCfg := NodeConfig{
		Datacenter:       mustGetString(cmd, "datacenter"),
		NodeClass:        mustGetString(cmd, "node-class"),
		NetworkInterface: networkInterface,
		ServerJoin:       serverJoin,
		HostVolumes:      hostVolumes,
		Encrypt:          mustGetString(cmd, "encrypt"),
		Address:          mustGetString(cmd, "address"),
		Advertise:        mustGetString(cmd, "advertise"),
		CAFile:           mustGetString(cmd, "ca-file"),
		CertFile:         mustGetString(cmd, "cert-file"),
		KeyFile:          mustGetString(cmd, "key-file"),
	}
	nodeCfg.ACL, _ = cmd.Flags().GetBool("acl")
	if cmd.Flags().Changed("advertise") && nomadUseTailscaleIP {
		nomadUseTailscaleIP = false
	}

	if dryRun {
		printDryRun(w, ex, nodeCfg, nomadVersion, useTailscale, packageInstallMethod, tsHostname, serverJoin, tsAuthKey != "", tsCreateAuthKey, tsKeyEphemeral, tsKeyReusable, tsKeyExpiry, nomadUseTailscaleIP, communityDrivers)
		return nil
	}

	// Resolve sudo password for preflight and install (flag > env var).
	sudoPassword, _ := cmd.Flags().GetString("password")
	if sudoPassword == "" {
		sudoPassword = os.Getenv("ABC_NODE_PASSWORD")
	}
	if sudoPassword == "" {
		if s, ok := ex.(*sshExec); ok && s.sudoPassword != "" {
			sudoPassword = s.sudoPassword
		}
	}

	// 1. Preflight checks
	var pf *PreflightResult
	if !skipPreflight {
		requirePkgManagerCheck := packageInstallMethod == packageInstallMethodPackageManager
		pf, err = RunPreflight(ctx, ex, w, sudoPassword, requirePkgManagerCheck)
		if err != nil {
			return err
		}
	} else {
		pf = &PreflightResult{OS: ex.OS(), Arch: ex.Arch(), HasSudo: true, InitSystem: initSystemFor(ex.OS())}
	}
	if useTailscale && !pf.TailscaleConnected && tsAuthKey == "" && !tsCreateAuthKey {
		return fmt.Errorf("missing Tailscale auth key: provide --tailscale-auth-key or enable --tailscale-create-auth-key")
	}

	// Resolve (or auto-create) Tailscale auth key right before bootstrap.
	if useTailscale && !pf.TailscaleConnected && tsAuthKey == "" {
		apiKey := strings.TrimSpace(os.Getenv("TAILSCALE_API_KEY"))
		if apiKey == "" {
			return fmt.Errorf("missing Tailscale credentials: set --tailscale-auth-key or export TAILSCALE_API_KEY")
		}
		fmt.Fprintf(w, "\n  Creating Tailscale auth key via API...\n")
		description := tsKeyDescription
		if description == "" {
			description = fmt.Sprintf("abc node add bootstrap (%s)", time.Now().UTC().Format(time.RFC3339))
		}
		tsAuthKey, err = CreateTailscaleAuthKey(ctx, TailscaleAuthKeyCreateRequest{
			APIKey:        apiKey,
			Reusable:      tsKeyReusable,
			Ephemeral:     tsKeyEphemeral,
			Preauthorized: tsKeyPreauthorized,
			Expiry:        tsKeyExpiry,
			Description:   description,
		})
		if err != nil {
			return fmt.Errorf("create Tailscale auth key: %w", err)
		}
		fmt.Fprintf(w, "    ✓ Created bootstrap key (ephemeral=%t reusable=%t expiry=%s)\n", tsKeyEphemeral, tsKeyReusable, tsKeyExpiry)
	}

	// 2. Tailscale (only when --tailscale flag is set)
	if useTailscale && !pf.TailscaleConnected {
		if pf.TailscaleInstalled {
			fmt.Fprintf(w, "\n  Joining tailnet...\n")
			args := "tailscale up --auth-key=" + tsAuthKey
			if tsHostname != "" {
				args += " --hostname=" + tsHostname
			}
			if err := ex.Run(ctx, "sudo "+args+" 2>&1 || "+args+" 2>&1", LineWriter(w, "    ")); err != nil {
				return fmt.Errorf("tailscale up: %w", err)
			}
		} else {
			if err := InstallTailscale(ctx, ex, tsAuthKey, tsHostname, packageInstallMethod, w); err != nil {
				return err
			}
		}
	}

	// 2b. Optionally derive Nomad advertise address from Tailscale.
	if nomadUseTailscaleIP && nodeCfg.Advertise == "" {
		tsIP, err := DetectTailscaleIPv4(ctx, ex)
		if err != nil {
			return fmt.Errorf("resolve Tailscale IPv4 for Nomad advertise address: %w", err)
		}
		nodeCfg.Advertise = tsIP
		fmt.Fprintf(w, "\n  Using Tailscale IP for Nomad advertise address: %s\n", tsIP)
	}

	// 3. Nomad
	installCfg := NomadInstallConfig{
		Version:       nomadVersion,
		InstallMethod: packageInstallMethod,
		NodeConfig:    nodeCfg,
		SkipEnable:    skipEnable,
		SkipStart:     skipStart,
	}
	if !pf.NomadInstalled {
		if err := InstallNomad(ctx, ex, installCfg, w); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "\n  Nomad already installed — skipping install.\n")
		if shouldRefreshNomadConfig(cmd, nomadUseTailscaleIP) {
			fmt.Fprintf(w, "  Updating Nomad configuration.\n")
			if err := ApplyNomadConfig(ctx, ex, installCfg, w); err != nil {
				return err
			}
		}
	}

	// 4. Verify: poll local Nomad agent
	if !skipStart {
		fmt.Fprintf(w, "\n  Verifying...\n")
		if err := waitForNomadAgent(ctx, ex, w); err != nil {
			if communityDrivers.Requested() {
				return fmt.Errorf("nomad agent is not healthy yet; cannot run community driver post-setup: %w", err)
			}
			fmt.Fprintf(w, "    ! Could not verify Nomad agent: %v\n", err)
			fmt.Fprintf(w, "    Check: sudo journalctl -u nomad -n 50\n")
		}
	}

	// 5. Experimental post-setup (after node has joined and is healthy)
	if communityDrivers.Requested() {
		printExperimentalFeatureNotice(w, "community driver post-setup")
		if err := InstallCommunityDrivers(ctx, ex, communityDrivers, w); err != nil {
			return err
		}

		postSetupNodeCfg := nodeCfg
		applyCommunityDriverNodeConfig(&postSetupNodeCfg, communityDrivers)
		postSetupCfg := installCfg
		postSetupCfg.NodeConfig = postSetupNodeCfg
		postSetupCfg.SkipStart = false

		fmt.Fprintf(w, "\n  Applying post-setup Nomad config for community drivers...\n")
		if err := ApplyNomadConfig(ctx, ex, postSetupCfg, w); err != nil {
			return err
		}
		fmt.Fprintf(w, "  Verifying after post-setup restart...\n")
		if err := waitForNomadAgent(ctx, ex, w); err != nil {
			fmt.Fprintf(w, "    ! Could not verify Nomad agent after post-setup: %v\n", err)
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

func printDryRun(w io.Writer, ex Executor, cfg NodeConfig, version string, useTailscale bool, packageInstallMethod, tsHostname string, serverJoin []string, hasExplicitTSKey, tsCreateAuthKey, tsKeyEphemeral, tsKeyReusable bool, tsKeyExpiry time.Duration, nomadUseTailscaleIP bool, communityDrivers communityDriverInstallConfig) {
	fmt.Fprintf(w, "\n  Dry-run plan:\n")
	fmt.Fprintf(w, "    Target:       %s/%s\n", ex.OS(), ex.Arch())
	fmt.Fprintf(w, "    Install mode: %s\n", packageInstallMethod)
	fmt.Fprintf(w, "    Datacenter:   %s\n", cfg.Datacenter)
	if cfg.NodeClass != "" {
		fmt.Fprintf(w, "    Node class:   %s\n", cfg.NodeClass)
	}
	if len(serverJoin) > 0 {
		fmt.Fprintf(w, "    Server join:  %s\n", strings.Join(serverJoin, ", "))
	}
	if cfg.NetworkInterface != "" {
		fmt.Fprintf(w, "    Net iface:    %s\n", cfg.NetworkInterface)
	}
	if len(cfg.HostVolumes) > 0 {
		fmt.Fprintf(w, "    Host volumes:\n")
		for _, v := range cfg.HostVolumes {
			fmt.Fprintf(w, "      - %s => %s (read_only=%t)\n", v.Name, v.Path, v.ReadOnly)
		}
	}
	if communityDrivers.Requested() {
		fmt.Fprintf(w, "    Community drivers (post-setup after node join):\n")
		for _, driver := range communityDrivers.Drivers {
			switch driver {
			case communityDriverContainerd:
				fmt.Fprintf(w, "      - %s (nerdctl-full %s, nomad-driver-containerd %s)\n", driver, communityDrivers.ContainerdNerdctlVersion, communityDrivers.ContainerdDriverVersion)
			default:
				fmt.Fprintf(w, "      - %s\n", driver)
			}
		}
	}
	if useTailscale {
		fmt.Fprintf(w, "    Tailscale:    install + tailscale up (%s)", packageInstallMethod)
		if tsHostname != "" {
			fmt.Fprintf(w, " --hostname=%s", tsHostname)
		}
		fmt.Fprintln(w)
		switch {
		case hasExplicitTSKey:
			fmt.Fprintf(w, "    TS auth key:  provided via --tailscale-auth-key\n")
		case tsCreateAuthKey:
			fmt.Fprintf(w, "    TS auth key:  will be auto-created via TAILSCALE_API_KEY (ephemeral=%t reusable=%t expiry=%s)\n", tsKeyEphemeral, tsKeyReusable, tsKeyExpiry)
		default:
			fmt.Fprintf(w, "    TS auth key:  missing (set --tailscale-auth-key or enable --tailscale-create-auth-key)\n")
		}
	} else {
		fmt.Fprintf(w, "    Tailscale:    off (direct-join mode)\n")
	}
	if nomadUseTailscaleIP && cfg.Advertise == "" {
		fmt.Fprintf(w, "    Advertise:    tailscale IPv4 (resolved at runtime)\n")
	} else if cfg.Advertise != "" {
		fmt.Fprintf(w, "    Advertise:    %s\n", cfg.Advertise)
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

func hostVolumesFromFlags(cmd *cobra.Command) ([]NomadHostVolume, error) {
	includeScratch, _ := cmd.Flags().GetBool("scratch-host-volume")
	scratchPath, _ := cmd.Flags().GetString("scratch-host-volume-path")
	rawHostVolumes, _ := cmd.Flags().GetStringArray("host-volume")

	volumes := make([]NomadHostVolume, 0, len(rawHostVolumes)+1)
	volumeIndex := make(map[string]int, len(rawHostVolumes)+1)

	upsert := func(v NomadHostVolume) {
		if idx, ok := volumeIndex[v.Name]; ok {
			volumes[idx] = v
			return
		}
		volumeIndex[v.Name] = len(volumes)
		volumes = append(volumes, v)
	}

	if includeScratch {
		path := strings.TrimSpace(scratchPath)
		if path == "" {
			return nil, fmt.Errorf("--scratch-host-volume-path cannot be empty when --scratch-host-volume is enabled")
		}
		upsert(NomadHostVolume{Name: "scratch", Path: path, ReadOnly: false})
	}

	for _, raw := range rawHostVolumes {
		v, err := parseHostVolumeFlag(raw)
		if err != nil {
			return nil, err
		}
		upsert(v)
	}

	return volumes, nil
}

func parseHostVolumeFlag(raw string) (NomadHostVolume, error) {
	entry := strings.TrimSpace(raw)
	if entry == "" {
		return NomadHostVolume{}, fmt.Errorf("empty --host-volume value")
	}

	parts := strings.SplitN(entry, "=", 2)
	if len(parts) != 2 {
		return NomadHostVolume{}, fmt.Errorf("--host-volume must be name=path[:read_only], got %q", raw)
	}
	name := strings.TrimSpace(parts[0])
	pathAndMode := strings.TrimSpace(parts[1])
	if name == "" || pathAndMode == "" {
		return NomadHostVolume{}, fmt.Errorf("--host-volume must be name=path[:read_only], got %q", raw)
	}

	path := pathAndMode
	readOnly := false
	if i := strings.LastIndex(pathAndMode, ":"); i > 0 && i < len(pathAndMode)-1 {
		if ro, err := strconv.ParseBool(strings.TrimSpace(pathAndMode[i+1:])); err == nil {
			readOnly = ro
			path = strings.TrimSpace(pathAndMode[:i])
		}
	}
	if path == "" {
		return NomadHostVolume{}, fmt.Errorf("host volume path cannot be empty in %q", raw)
	}

	return NomadHostVolume{
		Name:     name,
		Path:     path,
		ReadOnly: readOnly,
	}, nil
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
