package compute

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

const (
	communityDriverContainerd = "containerd"
	communityDriverExec2      = "exec2"

	defaultContainerdNerdctlVersion = "2.0.0"
	defaultContainerdDriverVersion  = "0.9.4"
	defaultExec2DriverVersion       = "0.1.1"
	defaultNomadPluginsDir          = "/opt/nomad/plugins"
	defaultContainerdCNIPath        = "/opt/cni/bin"
	defaultContainerdDriverRuntime  = "io.containerd.runc.v2"
	defaultContainerdStatsInterval  = "5s"
	containerdSystemdUnit           = `[Unit]
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

type communityDriverReleaseMetadata struct {
	SystemdUnitPath     string
	RuntimeReleaseOwner string
	RuntimeReleaseRepo  string
	DriverReleaseOwner  string
	DriverReleaseRepo   string
}

var communityDriverReleaseMetadataByName = map[string]communityDriverReleaseMetadata{
	communityDriverContainerd: {
		SystemdUnitPath:     "/etc/systemd/system/containerd.service",
		RuntimeReleaseOwner: "containerd",
		RuntimeReleaseRepo:  "nerdctl",
		DriverReleaseOwner:  "Roblox",
		DriverReleaseRepo:   "nomad-driver-containerd",
	},
	communityDriverExec2: {
		DriverReleaseOwner: "hashicorp",
		DriverReleaseRepo:  "nomad-driver-exec2",
	},
}

type communityDriverInstallConfig struct {
	Drivers                  []string
	ContainerdNerdctlVersion string
	ContainerdDriverVersion  string
	Exec2DriverVersion       string
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
	exec2DriverVersion, _ := cmd.Flags().GetString("exec2-version")
	exec2DriverVersion = normalizeReleaseVersion(exec2DriverVersion)
	if exec2DriverVersion == "" {
		exec2DriverVersion = defaultExec2DriverVersion
	}
	return communityDriverInstallConfig{
		Drivers:                  drivers,
		ContainerdNerdctlVersion: nerdctlVersion,
		ContainerdDriverVersion:  containerdDriverVersion,
		Exec2DriverVersion:       exec2DriverVersion,
	}, nil
}

func nerdctlReleaseAssetName(goarch, nerdctlVersion string) (string, error) {
	arch, err := nerdctlReleaseArch(goarch)
	if err != nil {
		return "", err
	}
	version := normalizeReleaseVersion(nerdctlVersion)
	if version == "" {
		return "", fmt.Errorf("containerd nerdctl version cannot be empty")
	}
	return fmt.Sprintf("nerdctl-full-%s-linux-%s.tar.gz", version, arch), nil
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
			case communityDriverExec2:
			default:
				return nil, fmt.Errorf("unsupported --community-driver %q (currently supported: %s, %s)", name, communityDriverContainerd, communityDriverExec2)
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

func releaseMetadataForDriver(driver string) (communityDriverReleaseMetadata, error) {
	metadata, ok := communityDriverReleaseMetadataByName[driver]
	if !ok {
		return communityDriverReleaseMetadata{}, fmt.Errorf("missing release metadata for community driver %q", driver)
	}
	return metadata, nil
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
	if !cfg.Requested() {
		return nil
	}
	if goos != "linux" {
		return fmt.Errorf("community driver install is currently supported only on linux targets")
	}
	return nil
}

func applyCommunityDriverNodeConfig(nodeCfg *NodeConfig, cfg communityDriverInstallConfig) {
	if nodeCfg == nil {
		return
	}
	if cfg.Has(communityDriverExec2) {
		if nodeCfg.PluginDir == "" {
			nodeCfg.PluginDir = defaultNomadPluginsDir
		}
		nodeCfg.EnableExec2Driver = true
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
		case communityDriverExec2:
			if err := installCommunityExec2(ctx, ex, cfg.Exec2DriverVersion, w); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported community driver %q", driver)
		}
	}
	return nil
}

func installCommunityContainerd(ctx context.Context, ex Executor, nerdctlVersion, containerdDriverVersion string, w io.Writer) error {
	metadata, err := releaseMetadataForDriver(communityDriverContainerd)
	if err != nil {
		return err
	}
	runtimePresent := containerdRuntimePresent(ctx, ex)
	driverPresent := containerdDriverPresent(ctx, ex)
	if runtimePresent && driverPresent {
		fmt.Fprintf(w, "\n  Containerd runtime and driver already present; skipping download.\n")
		if err := ensureContainerdServiceActive(ctx, ex, metadata.SystemdUnitPath, w); err != nil {
			return err
		}
		return nil
	}
	nerdctlAsset, err := nerdctlReleaseAssetName(ex.Arch(), nerdctlVersion)
	if err != nil {
		return err
	}
	nerdctlURL := githubReleaseDownloadURL(metadata.RuntimeReleaseOwner, metadata.RuntimeReleaseRepo, normalizeReleaseVersion(nerdctlVersion), nerdctlAsset)
	if resolvedURL, err := resolveGitHubReleaseAssetURL(ctx, metadata.RuntimeReleaseOwner, metadata.RuntimeReleaseRepo, nerdctlVersion, nerdctlAsset); err == nil {
		nerdctlURL = resolvedURL
	} else {
		fmt.Fprintf(w, "    ! Could not resolve %s from GitHub API (%v); using default release URL\n", nerdctlAsset, err)
	}
	steps, err := containerdInstallSteps(ex.Arch(), nerdctlVersion, nerdctlURL)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n  Installing community containerd runtime via nerdctl-full %s...\n", normalizeReleaseVersion(nerdctlVersion))
	if err := ex.Run(ctx, strings.Join(steps, " && "), LineWriter(w, "    ")); err != nil {
		return fmt.Errorf("install nerdctl-full bundle: %w", err)
	}
	if err := ensureContainerdServiceActive(ctx, ex, metadata.SystemdUnitPath, w); err != nil {
		return err
	}

	driverAsset, err := containerdDriverReleaseAsset(ex.Arch())
	if err != nil {
		return err
	}
	driverURL := githubReleaseDownloadURL(metadata.DriverReleaseOwner, metadata.DriverReleaseRepo, normalizeReleaseVersion(containerdDriverVersion), driverAsset)
	if resolvedURL, err := resolveGitHubReleaseAssetURL(ctx, metadata.DriverReleaseOwner, metadata.DriverReleaseRepo, containerdDriverVersion, driverAsset); err == nil {
		driverURL = resolvedURL
	} else {
		fmt.Fprintf(w, "    ! Could not resolve %s from GitHub API (%v); using default release URL\n", driverAsset, err)
	}
	driverSteps, err := containerdDriverInstallSteps(ex.Arch(), containerdDriverVersion, driverURL)
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

func containerdRuntimePresent(ctx context.Context, ex Executor) bool {
	return ex.Run(ctx, "command -v containerd >/dev/null 2>&1", io.Discard) == nil
}

func containerdDriverPresent(ctx context.Context, ex Executor) bool {
	return ex.Run(ctx, "test -x /opt/nomad/plugins/containerd-driver", io.Discard) == nil
}

func ensureContainerdServiceActive(ctx context.Context, ex Executor, unitPath string, w io.Writer) error {
	if err := ex.Run(ctx, renderContainerdSystemdUnitInstallCommand(unitPath), LineWriter(w, "    ")); err != nil {
		return fmt.Errorf("install containerd systemd unit: %w", err)
	}
	enableCmd := strings.Join([]string{
		"sudo chown root:root " + unitPath,
		"sudo systemctl daemon-reload",
		"sudo systemctl enable --now containerd",
		"sudo systemctl is-active --quiet containerd",
	}, " && ")
	if err := ex.Run(ctx, enableCmd, LineWriter(w, "    ")); err != nil {
		return fmt.Errorf("enable/start containerd service: %w", err)
	}
	fmt.Fprintf(w, "    ✓ containerd service active\n")
	return nil
}

func installCommunityExec2(ctx context.Context, ex Executor, driverVersion string, w io.Writer) error {
	version := normalizeReleaseVersion(driverVersion)
	if version == "" {
		return fmt.Errorf("exec2 driver version cannot be empty")
	}
	assetName, err := exec2ReleaseAssetName(ex.Arch(), version)
	if err != nil {
		return err
	}
	zipURL := exec2ReleaseAssetDownloadURL(version, assetName)
	shasumsURL := exec2ReleaseShasumsURL(version)
	if resolvedZipURL, resolvedShasumsURL, err := resolveExec2ReleaseURLs(ctx, version, assetName); err == nil {
		zipURL = resolvedZipURL
		shasumsURL = resolvedShasumsURL
	} else {
		fmt.Fprintf(w, "    ! Could not resolve exec2 release metadata from HashiCorp index (%v); using default release URLs\n", err)
	}

	fmt.Fprintf(w, "    Installing nomad-driver-exec2 %s...\n", version)
	zipData, err := fetchBytes(ctx, zipURL)
	if err != nil {
		return fmt.Errorf("download exec2 asset %s: %w", assetName, err)
	}
	sums, err := fetchText(ctx, shasumsURL)
	if err != nil {
		return fmt.Errorf("download exec2 checksums: %w", err)
	}
	expectedSHA := extractSHA(sums, assetName)
	if expectedSHA == "" {
		return fmt.Errorf("checksum for %s not found in %s", assetName, shasumsURL)
	}
	actualSum := sha256.Sum256(zipData)
	actualHex := hex.EncodeToString(actualSum[:])
	if !strings.EqualFold(actualHex, expectedSHA) {
		return fmt.Errorf("checksum mismatch for %s: got %s want %s", assetName, actualHex, expectedSHA)
	}
	bin, err := extractFileByBaseNameFromZip(zipData, "nomad-driver-exec2")
	if err != nil {
		return fmt.Errorf("extract nomad-driver-exec2 from %s: %w", assetName, err)
	}
	if err := ex.Run(ctx, fmt.Sprintf("sudo mkdir -p %s", defaultNomadPluginsDir), io.Discard); err != nil {
		return fmt.Errorf("create plugin directory %s: %w", defaultNomadPluginsDir, err)
	}
	pluginPath := fmt.Sprintf("%s/nomad-driver-exec2", defaultNomadPluginsDir)
	if err := ex.Upload(ctx, bytes.NewReader(bin), pluginPath, 0755); err != nil {
		return fmt.Errorf("upload exec2 plugin to %s: %w", pluginPath, err)
	}
	if err := ex.Run(ctx, fmt.Sprintf("sudo chown root:root %s && sudo chmod 0755 %s", pluginPath, pluginPath), io.Discard); err != nil {
		return fmt.Errorf("set ownership/permissions on %s: %w", pluginPath, err)
	}
	fmt.Fprintf(w, "    ✓ nomad-driver-exec2 installed in %s\n", defaultNomadPluginsDir)
	return nil
}

type hashicorpReleaseIndex struct {
	Versions map[string]hashicorpReleaseVersion `json:"versions"`
}

type hashicorpReleaseVersion struct {
	Builds     []hashicorpReleaseBuild `json:"builds"`
	ShasumsURL string                  `json:"shasums_url"`
}

type hashicorpReleaseBuild struct {
	Arch     string `json:"arch"`
	Filename string `json:"filename"`
	OS       string `json:"os"`
	URL      string `json:"url"`
}

func resolveExec2ReleaseURLs(ctx context.Context, version, assetName string) (zipURL, shasumsURL string, err error) {
	const exec2IndexURL = "https://releases.hashicorp.com/nomad-driver-exec2/index.json"
	data, err := fetchBytes(ctx, exec2IndexURL)
	if err != nil {
		return "", "", err
	}
	var idx hashicorpReleaseIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return "", "", fmt.Errorf("parse exec2 release index: %w", err)
	}
	ver, ok := idx.Versions[version]
	if !ok {
		return "", "", fmt.Errorf("exec2 version %s not found in release index", version)
	}
	if strings.TrimSpace(ver.ShasumsURL) == "" {
		return "", "", fmt.Errorf("exec2 version %s release index is missing shasums_url", version)
	}
	for _, build := range ver.Builds {
		if build.Filename == assetName && strings.TrimSpace(build.URL) != "" {
			return build.URL, ver.ShasumsURL, nil
		}
	}
	return "", "", fmt.Errorf("exec2 version %s does not contain asset %s", version, assetName)
}

func exec2ReleaseArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported arch for nomad-driver-exec2 release asset: %s", goarch)
	}
}

func exec2ReleaseAssetName(goarch, version string) (string, error) {
	arch, err := exec2ReleaseArch(goarch)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("nomad-driver-exec2_%s_linux_%s.zip", version, arch), nil
}

func exec2ReleaseAssetDownloadURL(version, assetName string) string {
	return fmt.Sprintf("https://releases.hashicorp.com/nomad-driver-exec2/%s/%s", version, assetName)
}

func exec2ReleaseShasumsURL(version string) string {
	return fmt.Sprintf("https://releases.hashicorp.com/nomad-driver-exec2/%s/nomad-driver-exec2_%s_SHA256SUMS", version, version)
}

func exec2DriverScriptInstallSteps(goarch, driverVersion string) ([]string, error) {
	version := normalizeReleaseVersion(driverVersion)
	if version == "" {
		return nil, fmt.Errorf("exec2 driver version cannot be empty")
	}
	assetName, err := exec2ReleaseAssetName(goarch, version)
	if err != nil {
		return nil, err
	}
	return []string{
		fmt.Sprintf("EXEC2_VERSION=%q", version),
		fmt.Sprintf("EXEC2_ASSET=%q", assetName),
		fmt.Sprintf("EXEC2_ZIP_URL=%q", exec2ReleaseAssetDownloadURL(version, assetName)),
		fmt.Sprintf("EXEC2_SHASUMS_URL=%q", exec2ReleaseShasumsURL(version)),
		"(command -v curl >/dev/null 2>&1 && curl -fL --retry 5 --retry-delay 1 --retry-all-errors --connect-timeout 20 --max-time 600 -sS \"${EXEC2_ZIP_URL}\" -o \"/tmp/exec2-driver.zip\") || (command -v wget >/dev/null 2>&1 && wget -q --tries=5 --timeout=30 -O \"/tmp/exec2-driver.zip\" \"${EXEC2_ZIP_URL}\")",
		"(command -v curl >/dev/null 2>&1 && curl -fL --retry 5 --retry-delay 1 --retry-all-errors --connect-timeout 20 --max-time 120 -sS \"${EXEC2_SHASUMS_URL}\" -o \"/tmp/exec2_SHA256SUMS\") || (command -v wget >/dev/null 2>&1 && wget -q --tries=5 --timeout=30 -O \"/tmp/exec2_SHA256SUMS\" \"${EXEC2_SHASUMS_URL}\")",
		"grep \"${EXEC2_ASSET}\" /tmp/exec2_SHA256SUMS | awk '{print $1\"  /tmp/exec2-driver.zip\"}' | sha256sum -c -",
		"rm -rf /tmp/exec2-driver-extract && mkdir -p /tmp/exec2-driver-extract",
		"(command -v unzip >/dev/null 2>&1 && unzip -o /tmp/exec2-driver.zip -d /tmp/exec2-driver-extract) || (command -v bsdtar >/dev/null 2>&1 && bsdtar -xf /tmp/exec2-driver.zip -C /tmp/exec2-driver-extract)",
		fmt.Sprintf("sudo mkdir -p %s", defaultNomadPluginsDir),
		fmt.Sprintf("sudo install -m 0755 /tmp/exec2-driver-extract/nomad-driver-exec2 %s/nomad-driver-exec2", defaultNomadPluginsDir),
		"rm -rf /tmp/exec2-driver.zip /tmp/exec2_SHA256SUMS /tmp/exec2-driver-extract",
	}, nil
}

func extractFileByBaseNameFromZip(zipData []byte, filename string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range r.File {
		if path.Base(f.Name) != filename {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s in zip: %w", f.Name, err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("%q not found in zip archive", filename)
}

func containerdInstallSteps(goarch, nerdctlVersion, nerdctlURL string) ([]string, error) {
	assetName, err := nerdctlReleaseAssetName(goarch, nerdctlVersion)
	if err != nil {
		return nil, err
	}
	version := normalizeReleaseVersion(nerdctlVersion)
	if version == "" {
		return nil, fmt.Errorf("containerd nerdctl version cannot be empty")
	}
	if strings.TrimSpace(nerdctlURL) == "" {
		metadata, err := releaseMetadataForDriver(communityDriverContainerd)
		if err != nil {
			return nil, err
		}
		nerdctlURL = githubReleaseDownloadURL(metadata.RuntimeReleaseOwner, metadata.RuntimeReleaseRepo, version, assetName)
	}
	return []string{
		fmt.Sprintf("NERDCTL_VERSION=%q", version),
		fmt.Sprintf("NERDCTL_TGZ=%q", assetName),
		fmt.Sprintf("NERDCTL_URL=%q", nerdctlURL),
		"(command -v curl >/dev/null 2>&1 && curl -fL --retry 5 --retry-delay 1 --retry-all-errors --connect-timeout 20 --max-time 1800 -sS \"${NERDCTL_URL}\" -o \"/tmp/${NERDCTL_TGZ}\") || (command -v wget >/dev/null 2>&1 && wget -q --tries=5 --timeout=30 -O \"/tmp/${NERDCTL_TGZ}\" \"${NERDCTL_URL}\")",
		"sudo tar -C /usr/local -xzf \"/tmp/${NERDCTL_TGZ}\"",
		"rm -f \"/tmp/${NERDCTL_TGZ}\"",
		"sudo mkdir -p /etc/containerd /etc/cni/net.d /opt/cni/bin",
		"if [ -d /usr/local/libexec/cni ]; then sudo cp -f /usr/local/libexec/cni/* /opt/cni/bin/; fi",
		"/usr/local/bin/containerd config default > /tmp/containerd-config.toml",
		"sudo install -m 0644 /tmp/containerd-config.toml /etc/containerd/config.toml",
		"rm -f /tmp/containerd-config.toml",
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

func containerdDriverInstallSteps(goarch, driverVersion, driverURL string) ([]string, error) {
	asset, err := containerdDriverReleaseAsset(goarch)
	if err != nil {
		return nil, err
	}
	version := normalizeReleaseVersion(driverVersion)
	if version == "" {
		return nil, fmt.Errorf("containerd driver version cannot be empty")
	}
	if strings.TrimSpace(driverURL) == "" {
		metadata, err := releaseMetadataForDriver(communityDriverContainerd)
		if err != nil {
			return nil, err
		}
		driverURL = githubReleaseDownloadURL(metadata.DriverReleaseOwner, metadata.DriverReleaseRepo, version, asset)
	}
	return []string{
		fmt.Sprintf("CONTAINERD_DRIVER_VERSION=%q", version),
		fmt.Sprintf("CONTAINERD_DRIVER_ASSET=%q", asset),
		fmt.Sprintf("CONTAINERD_DRIVER_URL=%q", driverURL),
		"(command -v curl >/dev/null 2>&1 && curl -fL --retry 5 --retry-delay 1 --retry-all-errors --connect-timeout 20 --max-time 300 -sS \"${CONTAINERD_DRIVER_URL}\" -o \"/tmp/containerd-driver\") || (command -v wget >/dev/null 2>&1 && wget -q --tries=5 --timeout=30 -O \"/tmp/containerd-driver\" \"${CONTAINERD_DRIVER_URL}\")",
		fmt.Sprintf("sudo mkdir -p %s", defaultNomadPluginsDir),
		fmt.Sprintf("sudo install -m 0755 \"/tmp/containerd-driver\" %s/containerd-driver", defaultNomadPluginsDir),
		"rm -f \"/tmp/containerd-driver\"",
	}, nil
}

func renderContainerdSystemdUnitInstallCommand(unitPath string) string {
	return strings.Join([]string{
		"CONTAINERD_UNIT_TMP=$(mktemp)",
		"cat > \"${CONTAINERD_UNIT_TMP}\" <<'UNIT'",
		strings.TrimRight(containerdSystemdUnit, "\n"),
		"UNIT",
		"sudo install -m 0644 \"${CONTAINERD_UNIT_TMP}\" " + unitPath,
		"rm -f \"${CONTAINERD_UNIT_TMP}\"",
	}, "\n")
}

func githubReleaseDownloadURL(owner, repo, version, asset string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/%s", owner, repo, version, asset)
}

func resolveGitHubReleaseAssetURL(ctx context.Context, owner, repo, version, assetName string) (string, error) {
	releaseVersion := normalizeReleaseVersion(version)
	if releaseVersion == "" {
		return "", fmt.Errorf("release version cannot be empty for %s/%s", owner, repo)
	}
	tagCandidates := []string{"v" + releaseVersion, releaseVersion}
	var lastErr error
	for _, tag := range tagCandidates {
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
		body, err := fetchBytes(ctx, apiURL)
		if err != nil {
			lastErr = fmt.Errorf("fetch release metadata for %s/%s tag %s: %w", owner, repo, tag, err)
			continue
		}
		var release struct {
			Assets []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			} `json:"assets"`
		}
		if err := json.Unmarshal(body, &release); err != nil {
			lastErr = fmt.Errorf("parse release metadata for %s/%s tag %s: %w", owner, repo, tag, err)
			continue
		}
		for _, asset := range release.Assets {
			if asset.Name == assetName && strings.TrimSpace(asset.BrowserDownloadURL) != "" {
				return asset.BrowserDownloadURL, nil
			}
		}
		return "", fmt.Errorf("release %s/%s tag %s does not contain asset %q", owner, repo, tag, assetName)
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("unable to resolve release asset %q for %s/%s", assetName, owner, repo)
}

func printCommunityDriverPostSetupScriptSection(w io.Writer, goos, goarch string, cfg communityDriverInstallConfig, cfgPath, finalHCL string, rewriteNomadConfig bool) error {
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
		metadata, err := releaseMetadataForDriver(communityDriverContainerd)
		if err != nil {
			return err
		}
		steps, err := containerdInstallSteps(goarch, cfg.ContainerdNerdctlVersion, "")
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "# Install containerd runtime via nerdctl-full\n")
		for _, step := range steps {
			fmt.Fprintln(w, step)
		}
		fmt.Fprintln(w, renderContainerdSystemdUnitInstallCommand(metadata.SystemdUnitPath))
		fmt.Fprintf(w, "sudo chown root:root %s\n", metadata.SystemdUnitPath)
		fmt.Fprintln(w, "sudo systemctl daemon-reload")
		fmt.Fprintln(w, "sudo systemctl enable --now containerd")
		fmt.Fprintln(w, "sudo systemctl is-active --quiet containerd && echo '✓ containerd active'")
		driverSteps, err := containerdDriverInstallSteps(goarch, cfg.ContainerdDriverVersion, "")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, "# Install nomad-driver-containerd plugin binary")
		for _, step := range driverSteps {
			fmt.Fprintln(w, step)
		}
	}
	if cfg.Has(communityDriverExec2) {
		steps, err := exec2DriverScriptInstallSteps(goarch, cfg.Exec2DriverVersion)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, "# Install nomad-driver-exec2 plugin binary")
		for _, step := range steps {
			fmt.Fprintln(w, step)
		}
	}
	if rewriteNomadConfig {
		printPostSetupNomadConfigRewriteAndRestart(w, cfgPath, finalHCL)
	}
	return nil
}
