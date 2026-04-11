package node

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

const (
	communityDriverContainerd = "containerd"

	defaultContainerdNerdctlVersion = "2.0.0"
	defaultContainerdDriverVersion  = "0.9.4"
	defaultNomadPluginsDir          = "/opt/nomad/plugins"
	defaultContainerdCNIPath        = "/opt/cni/bin"
	defaultContainerdDriverRuntime  = "io.containerd.runc.v2"
	defaultContainerdStatsInterval  = "5s"

	containerdSystemdUnitPath = "/etc/systemd/system/containerd.service"
	containerdSystemdUnit     = `[Unit]
Description=containerd container runtime
Documentation=https://containerd.io
After=network.target local-fs.target

[Service]
ExecStart=/usr/local/bin/containerd
Delegate=yes
KillMode=process
Restart=always
RestartSec=5
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
OOMScoreAdjust=-999

[Install]
WantedBy=multi-user.target
`
)

type communityDriverInstallConfig struct {
	Drivers                  []string
	ContainerdNerdctlVersion string
	ContainerdDriverVersion  string
}

func (c communityDriverInstallConfig) Requested() bool {
	return len(c.Drivers) > 0
}

func (c communityDriverInstallConfig) Has(name string) bool {
	for _, driver := range c.Drivers {
		if driver == name {
			return true
		}
	}
	return false
}

func communityDriverInstallConfigFromFlags(cmd *cobra.Command) (communityDriverInstallConfig, error) {
	rawDrivers, _ := cmd.Flags().GetStringArray("community-driver")
	drivers, err := normalizeCommunityDrivers(rawDrivers)
	if err != nil {
		return communityDriverInstallConfig{}, err
	}
	nerdctlVersion, _ := cmd.Flags().GetString("containerd-nerdctl-version")
	nerdctlVersion = normalizeReleaseVersion(nerdctlVersion)
	if nerdctlVersion == "" {
		nerdctlVersion = defaultContainerdNerdctlVersion
	}
	containerdDriverVersion, _ := cmd.Flags().GetString("containerd-driver-version")
	containerdDriverVersion = normalizeReleaseVersion(containerdDriverVersion)
	if containerdDriverVersion == "" {
		containerdDriverVersion = defaultContainerdDriverVersion
	}
	return communityDriverInstallConfig{
		Drivers:                  drivers,
		ContainerdNerdctlVersion: nerdctlVersion,
		ContainerdDriverVersion:  containerdDriverVersion,
	}, nil
}

func normalizeCommunityDrivers(raw []string) ([]string, error) {
	seen := make(map[string]struct{})
	drivers := make([]string, 0, len(raw))
	for _, entry := range raw {
		for _, token := range strings.Split(entry, ",") {
			name := strings.ToLower(strings.TrimSpace(token))
			if name == "" {
				continue
			}
			switch name {
			case communityDriverContainerd:
			default:
				return nil, fmt.Errorf("unsupported --community-driver %q (currently supported: %s)", name, communityDriverContainerd)
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			drivers = append(drivers, name)
		}
	}
	return drivers, nil
}

func normalizeReleaseVersion(v string) string {
	trimmed := strings.TrimSpace(v)
	trimmed = strings.TrimPrefix(trimmed, "v")
	return trimmed
}

func ensureExperimentalFeatureEnabled(cmd *cobra.Command, enabledByRequest bool, featureName string) error {
	if !enabledByRequest {
		return nil
	}
	if utils.ExpFromCmd(cmd) {
		return nil
	}
	return fmt.Errorf("%s is not in stable mode yet; enable experimental mode with --exp or %s=1", featureName, utils.ExperimentalModeEnvVar)
}

func printExperimentalFeatureNotice(w io.Writer, featureName string) {
	fmt.Fprintf(w, "\n  [experimental] %s is enabled; behavior may change in future releases.\n", featureName)
}

func validateCommunityDriverTarget(goos string, cfg communityDriverInstallConfig) error {
	if !cfg.Has(communityDriverContainerd) {
		return nil
	}
	if goos != "linux" {
		return fmt.Errorf("community driver %q install is currently supported only on linux targets", communityDriverContainerd)
	}
	return nil
}

func applyCommunityDriverNodeConfig(nodeCfg *NodeConfig, cfg communityDriverInstallConfig) {
	if nodeCfg == nil {
		return
	}
	if cfg.Has(communityDriverContainerd) {
		if nodeCfg.CNIPath == "" {
			nodeCfg.CNIPath = defaultContainerdCNIPath
		}
		if nodeCfg.PluginDir == "" {
			nodeCfg.PluginDir = defaultNomadPluginsDir
		}
		nodeCfg.EnableContainerdDriver = true
		if nodeCfg.ContainerdDriverRuntime == "" {
			nodeCfg.ContainerdDriverRuntime = defaultContainerdDriverRuntime
		}
		if nodeCfg.ContainerdDriverStatsInterval == "" {
			nodeCfg.ContainerdDriverStatsInterval = defaultContainerdStatsInterval
		}
	}
}

func InstallCommunityDrivers(ctx context.Context, ex Executor, cfg communityDriverInstallConfig, w io.Writer) error {
	if !cfg.Requested() {
		return nil
	}
	if err := validateCommunityDriverTarget(ex.OS(), cfg); err != nil {
		return err
	}
	for _, driver := range cfg.Drivers {
		switch driver {
		case communityDriverContainerd:
			if err := installCommunityContainerd(ctx, ex, cfg.ContainerdNerdctlVersion, cfg.ContainerdDriverVersion, w); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported community driver %q", driver)
		}
	}
	return nil
}

func installCommunityContainerd(ctx context.Context, ex Executor, nerdctlVersion, containerdDriverVersion string, w io.Writer) error {
	steps, err := containerdInstallSteps(ex.Arch(), nerdctlVersion)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n  Installing community containerd runtime via nerdctl-full %s...\n", normalizeReleaseVersion(nerdctlVersion))
	if err := ex.Run(ctx, strings.Join(steps, " && "), LineWriter(w, "    ")); err != nil {
		return fmt.Errorf("install nerdctl-full bundle: %w", err)
	}

	if err := ex.Run(ctx, renderContainerdSystemdUnitInstallCommand(), LineWriter(w, "    ")); err != nil {
		return fmt.Errorf("install containerd systemd unit: %w", err)
	}

	enableCmd := strings.Join([]string{
		"sudo chown root:root " + containerdSystemdUnitPath,
		"sudo systemctl daemon-reload",
		"sudo systemctl enable --now containerd",
		"sudo systemctl is-active --quiet containerd",
	}, " && ")
	if err := ex.Run(ctx, enableCmd, LineWriter(w, "    ")); err != nil {
		return fmt.Errorf("enable/start containerd service: %w", err)
	}
	fmt.Fprintf(w, "    ✓ containerd service active\n")

	driverSteps, err := containerdDriverInstallSteps(ex.Arch(), containerdDriverVersion)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "    Installing nomad-driver-containerd %s...\n", normalizeReleaseVersion(containerdDriverVersion))
	if err := ex.Run(ctx, strings.Join(driverSteps, " && "), LineWriter(w, "    ")); err != nil {
		return fmt.Errorf("install nomad-driver-containerd binary: %w", err)
	}
	fmt.Fprintf(w, "    ✓ nomad-driver-containerd installed in %s\n", defaultNomadPluginsDir)
	return nil
}

func containerdInstallSteps(goarch, nerdctlVersion string) ([]string, error) {
	arch, err := nerdctlReleaseArch(goarch)
	if err != nil {
		return nil, err
	}
	version := normalizeReleaseVersion(nerdctlVersion)
	if version == "" {
		return nil, fmt.Errorf("containerd nerdctl version cannot be empty")
	}
	return []string{
		fmt.Sprintf("NERDCTL_VERSION=%q", version),
		fmt.Sprintf("NERDCTL_ARCH=%q", arch),
		"NERDCTL_TGZ=\"nerdctl-full-${NERDCTL_VERSION}-linux-${NERDCTL_ARCH}.tar.gz\"",
		"NERDCTL_URL=\"https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VERSION}/${NERDCTL_TGZ}\"",
		"curl -fsSL \"${NERDCTL_URL}\" -o \"/tmp/${NERDCTL_TGZ}\"",
		"sudo tar -C /usr/local -xzf \"/tmp/${NERDCTL_TGZ}\"",
		"rm -f \"/tmp/${NERDCTL_TGZ}\"",
		"sudo mkdir -p /etc/containerd /etc/cni/net.d /opt/cni/bin",
		"if [ -d /usr/local/libexec/cni ]; then sudo cp -f /usr/local/libexec/cni/* /opt/cni/bin/; fi",
		"sudo /usr/local/bin/containerd config default | sudo tee /etc/containerd/config.toml >/dev/null",
	}, nil
}

func nerdctlReleaseArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported arch for containerd nerdctl-full bundle: %s", goarch)
	}
}

func containerdDriverReleaseAsset(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "containerd-driver", nil
	case "arm64":
		return "containerd-driver-arm64", nil
	default:
		return "", fmt.Errorf("unsupported arch for nomad-driver-containerd release asset: %s", goarch)
	}
}

func containerdDriverInstallSteps(goarch, driverVersion string) ([]string, error) {
	asset, err := containerdDriverReleaseAsset(goarch)
	if err != nil {
		return nil, err
	}
	version := normalizeReleaseVersion(driverVersion)
	if version == "" {
		return nil, fmt.Errorf("containerd driver version cannot be empty")
	}
	return []string{
		fmt.Sprintf("CONTAINERD_DRIVER_VERSION=%q", version),
		fmt.Sprintf("CONTAINERD_DRIVER_ASSET=%q", asset),
		"CONTAINERD_DRIVER_URL=\"https://github.com/Roblox/nomad-driver-containerd/releases/download/v${CONTAINERD_DRIVER_VERSION}/${CONTAINERD_DRIVER_ASSET}\"",
		"curl -fsSL \"${CONTAINERD_DRIVER_URL}\" -o \"/tmp/containerd-driver\"",
		fmt.Sprintf("sudo mkdir -p %s", defaultNomadPluginsDir),
		fmt.Sprintf("sudo install -m 0755 \"/tmp/containerd-driver\" %s/containerd-driver", defaultNomadPluginsDir),
		"rm -f \"/tmp/containerd-driver\"",
	}, nil
}

func renderContainerdSystemdUnitInstallCommand() string {
	return strings.Join([]string{
		"sudo tee " + containerdSystemdUnitPath + " >/dev/null <<'UNIT'",
		strings.TrimRight(containerdSystemdUnit, "\n"),
		"UNIT",
	}, "\n")
}

func printCommunityDriverPostSetupScriptSection(w io.Writer, goos, goarch string, cfg communityDriverInstallConfig, cfgPath, finalHCL string) error {
	if !cfg.Requested() {
		return nil
	}
	if err := validateCommunityDriverTarget(goos, cfg); err != nil {
		return err
	}
	fmt.Fprintf(w, "# ── 7. Experimental post-setup: community drivers ───────────────────────────\n")
	fmt.Fprintf(w, "# [experimental] Run after the node has joined the cluster and is healthy.\n")
	fmt.Fprintf(w, "# [experimental] Requested community driver setup may change in future releases.\n")

	if cfg.Has(communityDriverContainerd) {
		steps, err := containerdInstallSteps(goarch, cfg.ContainerdNerdctlVersion)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "# Install containerd runtime via nerdctl-full\n")
		for _, step := range steps {
			fmt.Fprintln(w, step)
		}
		fmt.Fprintln(w, "sudo tee /etc/systemd/system/containerd.service > /dev/null <<'UNIT'")
		fmt.Fprint(w, containerdSystemdUnit)
		fmt.Fprintln(w, "UNIT")
		fmt.Fprintln(w, "sudo chown root:root /etc/systemd/system/containerd.service")
		fmt.Fprintln(w, "sudo systemctl daemon-reload")
		fmt.Fprintln(w, "sudo systemctl enable --now containerd")
		fmt.Fprintln(w, "sudo systemctl is-active --quiet containerd && echo '✓ containerd active'")

		driverSteps, err := containerdDriverInstallSteps(goarch, cfg.ContainerdDriverVersion)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, "# Install nomad-driver-containerd plugin binary")
		for _, step := range driverSteps {
			fmt.Fprintln(w, step)
		}
		fmt.Fprintln(w, "# Rewrite Nomad config with containerd-driver plugin and restart Nomad")
		fmt.Fprintf(w, "sudo tee \"%s\" > /dev/null <<'HCL'\n", cfgPath)
		fmt.Fprint(w, finalHCL)
		fmt.Fprintln(w, "HCL")
		fmt.Fprintf(w, "sudo chown root:root \"%s\"\n", cfgPath)
		fmt.Fprintf(w, "sudo chmod 640 \"%s\"\n", cfgPath)
		fmt.Fprintln(w, "sudo systemctl restart nomad || sudo systemctl start nomad")
		fmt.Fprintln(w, "echo 'Waiting for Nomad agent after post-setup restart...'")
		fmt.Fprintln(w, "for i in $(seq 1 20); do")
		fmt.Fprintln(w, "  curl -sf http://127.0.0.1:4646/v1/agent/self > /dev/null 2>&1 && echo '✓ Nomad agent healthy' && break")
		fmt.Fprintln(w, "  echo \"  attempt $i/20 — retrying in 3s...\"")
		fmt.Fprintln(w, "  sleep 3")
		fmt.Fprintln(w, "done")
	}
	fmt.Fprintln(w)
	return nil
}
