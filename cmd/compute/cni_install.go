package compute

import (
	"context"
	"fmt"
	"io"
	"strings"
)

const (
	defaultCNIPluginsVersion = "1.6.2"
	cniPluginsInstallDir     = "/opt/cni/bin"
	cniPluginsCNINetDir      = "/etc/cni/net.d"
	// Nomad's Linux bridge fingerprinter (client/fingerprint/bridge_linux.go) only
	// advertises network mode "bridge" when the kernel "bridge" module is detectable.
	// Without that fingerprint, the scheduler rejects bridge jobs with Constraint
	// "missing network" even when CNI binaries exist under cni_path.
	nomadBridgeModuleLoadFile = "/etc/modules-load.d/nomad-bridge.conf"
)

// cniPluginReleaseArch maps a Go GOARCH value to the CNI release arch string.
func cniPluginReleaseArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported arch for CNI plugins: %q (supported: amd64, arm64)", goarch)
	}
}

// cniPluginAssetName returns the tarball filename for the given arch and version,
// e.g. "cni-plugins-linux-amd64-v1.6.2.tgz".
func cniPluginAssetName(goarch, version string) (string, error) {
	arch, err := cniPluginReleaseArch(goarch)
	if err != nil {
		return "", err
	}
	v := normalizeReleaseVersion(version)
	if v == "" {
		return "", fmt.Errorf("CNI plugin version cannot be empty")
	}
	return fmt.Sprintf("cni-plugins-linux-%s-v%s.tgz", arch, v), nil
}

// cniPluginDownloadURL returns the GitHub release download URL for the tarball.
func cniPluginDownloadURL(goarch, version string) (string, error) {
	assetName, err := cniPluginAssetName(goarch, version)
	if err != nil {
		return "", err
	}
	v := normalizeReleaseVersion(version)
	return fmt.Sprintf("https://github.com/containernetworking/plugins/releases/download/v%s/%s", v, assetName), nil
}

// cniPluginInstallSteps returns a list of bash commands that:
//  1. Create /opt/cni/bin and /etc/cni/net.d
//  2. Download the CNI reference plugins tarball
//  3. Verify the sha256 checksum
//  4. Extract binaries into /opt/cni/bin
//  5. Clean up temp files
//
// Per https://developer.hashicorp.com/nomad/docs/networking/cni#install-cni-reference-plugins
//
// The upstream tarball also ships LICENSE and README.md into the same directory.
// Nomad's CNI plugin fingerprinter skips non-executables (see client logs:
// "unexpected non-executable in cni plugin directory"). Strip those files after
// every extract. (Scheduler Constraint "missing network" for mode=bridge is
// usually the Linux bridge kernel module fingerprint — see bridgeKernelModuleSetupCmd.)
func cniPluginInstallSteps(goarch, version string) ([]string, error) {
	arch, err := cniPluginReleaseArch(goarch)
	if err != nil {
		return nil, err
	}
	v := normalizeReleaseVersion(version)
	if v == "" {
		return nil, fmt.Errorf("CNI plugin version cannot be empty")
	}
	assetName := fmt.Sprintf("cni-plugins-linux-%s-v%s.tgz", arch, v)
	downloadURL := fmt.Sprintf("https://github.com/containernetworking/plugins/releases/download/v%s/%s", v, assetName)
	sha256URL := downloadURL + ".sha256"

	return []string{
		fmt.Sprintf("CNI_VERSION=%q", v),
		fmt.Sprintf("CNI_ARCH=%q", arch),
		fmt.Sprintf("CNI_ASSET=%q", assetName),
		fmt.Sprintf("CNI_URL=%q", downloadURL),
		fmt.Sprintf("CNI_SHA256_URL=%q", sha256URL),
		fmt.Sprintf("sudo mkdir -p %s %s", cniPluginsInstallDir, cniPluginsCNINetDir),
		`(command -v curl >/dev/null 2>&1 && curl -fL --retry 5 --retry-delay 1 --retry-all-errors --connect-timeout 20 --max-time 300 -sS "${CNI_URL}" -o "/tmp/${CNI_ASSET}") || (command -v wget >/dev/null 2>&1 && wget -q --tries=5 --timeout=60 -O "/tmp/${CNI_ASSET}" "${CNI_URL}")`,
		`(command -v curl >/dev/null 2>&1 && curl -fL --retry 3 --connect-timeout 10 -sS "${CNI_SHA256_URL}" -o "/tmp/${CNI_ASSET}.sha256") || (command -v wget >/dev/null 2>&1 && wget -q -O "/tmp/${CNI_ASSET}.sha256" "${CNI_SHA256_URL}")`,
		`echo "$(cat /tmp/${CNI_ASSET}.sha256)  /tmp/${CNI_ASSET}" | sha256sum -c -`,
		fmt.Sprintf(`sudo tar -C %s -xzf "/tmp/${CNI_ASSET}"`, cniPluginsInstallDir),
		fmt.Sprintf(`sudo rm -f %s/LICENSE %s/README.md`, cniPluginsInstallDir, cniPluginsInstallDir),
		`rm -f "/tmp/${CNI_ASSET}" "/tmp/${CNI_ASSET}.sha256"`,
	}, nil
}

// cniSanitizeInstallDirCmd removes non-plugin files shipped in the official CNI
// tarball that break Nomad's CNI fingerprint if left in cni_path.
func cniSanitizeInstallDirCmd() string {
	return fmt.Sprintf("sudo rm -f %s/LICENSE %s/README.md", cniPluginsInstallDir, cniPluginsInstallDir)
}

// bridgeKernelModuleSetupCmd loads the bridge module now (best-effort) and persists
// it for boot so Nomad's bridge fingerprinter enables bridge network placement.
func bridgeKernelModuleSetupCmd() string {
	return "sudo modprobe bridge 2>/dev/null || true; echo bridge | sudo tee " + nomadBridgeModuleLoadFile + " >/dev/null"
}

// cniPluginsPresent checks whether CNI plugins are already installed by looking
// for the canonical 'loopback' plugin binary in /opt/cni/bin.
func cniPluginsPresent(ctx context.Context, ex Executor) bool {
	return ex.Run(ctx, fmt.Sprintf("test -x %s/loopback", cniPluginsInstallDir), io.Discard) == nil
}

// InstallCNIPlugins downloads and installs the containernetworking/plugins CNI
// reference plugins onto the target executor. It is idempotent: if the loopback
// plugin binary is already present in /opt/cni/bin the install is skipped.
//
// See: https://developer.hashicorp.com/nomad/docs/networking/cni#install-cni-reference-plugins
func InstallCNIPlugins(ctx context.Context, ex Executor, version string, w io.Writer) error {
	if ex.OS() != "linux" {
		return fmt.Errorf("CNI plugins are only supported on Linux (target OS: %s)", ex.OS())
	}
	v := strings.TrimSpace(version)
	if v == "" {
		v = defaultCNIPluginsVersion
	}
	if cniPluginsPresent(ctx, ex) {
		fmt.Fprintf(w, "    CNI plugins already present in %s; skipping download.\n", cniPluginsInstallDir)
		if err := ex.Run(ctx, cniSanitizeInstallDirCmd(), LineWriter(w, "      ")); err != nil {
			return fmt.Errorf("sanitize CNI plugin dir (remove LICENSE/README from tarball): %w", err)
		}
		fmt.Fprintf(w, "    ✓ Sanitized %s for Nomad CNI fingerprint (removed LICENSE/README if present)\n", cniPluginsInstallDir)
		if err := ex.Run(ctx, bridgeKernelModuleSetupCmd(), LineWriter(w, "      ")); err != nil {
			return fmt.Errorf("enable Linux bridge kernel module for Nomad: %w", err)
		}
		fmt.Fprintf(w, "    ✓ Linux bridge module load configured (%s); restart Nomad if bridge scheduling was still disabled\n", nomadBridgeModuleLoadFile)
		return nil
	}
	steps, err := cniPluginInstallSteps(ex.Arch(), v)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "    Installing CNI reference plugins v%s → %s...\n", normalizeReleaseVersion(v), cniPluginsInstallDir)
	if err := ex.Run(ctx, strings.Join(steps, " && "), LineWriter(w, "      ")); err != nil {
		return fmt.Errorf("install CNI plugins: %w", err)
	}
	fmt.Fprintf(w, "    ✓ CNI plugins installed in %s\n", cniPluginsInstallDir)
	if err := ex.Run(ctx, bridgeKernelModuleSetupCmd(), LineWriter(w, "      ")); err != nil {
		return fmt.Errorf("enable Linux bridge kernel module for Nomad: %w", err)
	}
	fmt.Fprintf(w, "    ✓ Linux bridge module load configured (%s); restart Nomad to refresh bridge fingerprint\n", nomadBridgeModuleLoadFile)
	return nil
}
