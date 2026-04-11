package node

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"
)

// printSetupScript emits a complete, self-contained shell script to w covering
// every step that runInstall would perform on the target machine:
//
//  1. (Optional) Tailscale install + tailscale up
//  2. Nomad binary download, SHA256 verify, install
//  3. Nomad client HCL config
//  4. Service registration (systemd / launchd / Windows sc.exe instructions)
//  5. Health-check loop
//
// No executor or network connection is required — output is pure text generation.
// The script is valid bash (Linux/macOS) or a comment block (Windows).
func printSetupScript(
	w io.Writer,
	goos, goarch string,
	cfg NomadInstallConfig,
	useTailscale bool,
	tsKey, tsHostname string,
	packageInstallMethod string,
	tsCreateAuthKey bool,
	tsKeyEphemeral, tsKeyReusable bool,
	tsKeyExpiry time.Duration,
	autoNomadAdvertise bool,
	skipEnable, skipStart bool,
) error {
	version := cfg.Version
	if version == "" {
		version = defaultNomadVersion
	}

	releaseOS := map[string]string{"linux": "linux", "darwin": "darwin", "windows": "windows"}[goos]
	if releaseOS == "" {
		return fmt.Errorf("unsupported OS for script generation: %s", goos)
	}
	releaseArch := map[string]string{
		"amd64": "amd64", "arm64": "arm64", "386": "386", "arm": "arm",
	}[goarch]
	if releaseArch == "" {
		return fmt.Errorf("unsupported arch for script generation: %s", goarch)
	}

	binPath, cfgDir, cfgPath, dataDir := nomadPaths(goos)
	hostVolumeDirs := hostVolumePaths(cfg.NodeConfig.HostVolumes)
	hclContent := GenerateClientHCL(cfg.NodeConfig)
	if autoNomadAdvertise {
		hclContent = strings.ReplaceAll(hclContent, "$${NOMAD_ADVERTISE}", "${NOMAD_ADVERTISE}")
	}

	// ── Header ────────────────────────────────────────────────────────────────
	if goos == "windows" {
		fmt.Fprintf(w, "# abc node add — generated setup script\n")
		fmt.Fprintf(w, "# Generated:  %s\n", time.Now().UTC().Format(time.RFC3339))
		fmt.Fprintf(w, "# Target:     %s/%s\n", goos, goarch)
		if cfg.NodeConfig.Datacenter != "" {
			fmt.Fprintf(w, "# Datacenter: %s\n", cfg.NodeConfig.Datacenter)
		}
		if cfg.NodeConfig.NodeClass != "" {
			fmt.Fprintf(w, "# Node class: %s\n", cfg.NodeConfig.NodeClass)
		}
		fmt.Fprintln(w)
		printWindowsScript(w, version, releaseOS, releaseArch, binPath, cfgPath, dataDir, hostVolumeDirs, hclContent, useTailscale, tsKey, tsHostname, skipEnable, skipStart)
		return nil
	}

	fmt.Fprintf(w, "#!/usr/bin/env bash\n")
	fmt.Fprintf(w, "# abc node add — generated setup script\n")
	fmt.Fprintf(w, "# Generated:  %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "# Target:     %s/%s\n", goos, goarch)
	if cfg.NodeConfig.Datacenter != "" {
		fmt.Fprintf(w, "# Datacenter: %s\n", cfg.NodeConfig.Datacenter)
	}
	if cfg.NodeConfig.NodeClass != "" {
		fmt.Fprintf(w, "# Node class: %s\n", cfg.NodeConfig.NodeClass)
	}
	fmt.Fprintf(w, "set -euo pipefail\n")
	fmt.Fprintln(w)

	// ── 1. Tailscale ──────────────────────────────────────────────────────────
	fmt.Fprintf(w, "# ── 1. Tailscale ────────────────────────────────────────────────────────────\n")
	if useTailscale {
		if goos == "linux" {
			switch packageInstallMethod {
			case packageInstallMethodPackageManager:
				fmt.Fprintf(w, "curl -fsSL https://tailscale.com/install.sh | sudo sh\n")
			default:
				fmt.Fprintf(w, "TS_BASE=https://pkgs.tailscale.com/stable\n")
				fmt.Fprintf(w, "TS_ARCH=%s\n", releaseArch)
				fmt.Fprintf(w, "TS_META=$(curl -fsSL \"${TS_BASE}/?mode=json\" | tr -d '\\n')\n")
				fmt.Fprintf(w, "TS_TGZ=$(printf '%%s' \"$TS_META\" | sed -E \"s/.*\\\"Tarballs\\\":\\{[^}]*\\\"${TS_ARCH}\\\":\\\"([^\\\"]+)\\\".*/\\1/\")\n")
				fmt.Fprintf(w, "if [ -z \"$TS_TGZ\" ] || [ \"$TS_TGZ\" = \"$TS_META\" ]; then echo \"Could not resolve tailscale static tarball for ${TS_ARCH}\"; exit 1; fi\n")
				fmt.Fprintf(w, "curl -fsSL \"${TS_BASE}/${TS_TGZ}\" -o \"/tmp/${TS_TGZ}\"\n")
				fmt.Fprintf(w, "curl -fsSL \"${TS_BASE}/${TS_TGZ}.sha256\" -o \"/tmp/${TS_TGZ}.sha256\"\n")
				fmt.Fprintf(w, "echo \"$(cat /tmp/${TS_TGZ}.sha256)  /tmp/${TS_TGZ}\" | sha256sum -c -\n")
				fmt.Fprintf(w, "TS_TMP_DIR=$(mktemp -d)\n")
				fmt.Fprintf(w, "tar -xzf \"/tmp/${TS_TGZ}\" -C \"$TS_TMP_DIR\"\n")
				fmt.Fprintf(w, "sudo mkdir -p /usr/local/bin /var/lib/tailscale\n")
				fmt.Fprintf(w, "sudo cp \"$TS_TMP_DIR\"/tailscale_*/tailscale /usr/local/bin/tailscale\n")
				fmt.Fprintf(w, "sudo cp \"$TS_TMP_DIR\"/tailscale_*/tailscaled /usr/local/bin/tailscaled\n")
				fmt.Fprintf(w, "sudo chown root:root /usr/local/bin/tailscale /usr/local/bin/tailscaled\n")
				fmt.Fprintf(w, "sudo chmod 755 /usr/local/bin/tailscale /usr/local/bin/tailscaled\n")
				fmt.Fprintf(w, "sudo tee /etc/systemd/system/tailscaled.service > /dev/null <<'UNIT'\n")
				fmt.Fprint(w, tailscaledSystemdUnit)
				fmt.Fprintf(w, "UNIT\n")
				fmt.Fprintf(w, "sudo chown root:root /etc/systemd/system/tailscaled.service\n")
				fmt.Fprintf(w, "sudo systemctl daemon-reload\n")
				fmt.Fprintf(w, "sudo systemctl enable tailscaled\n")
				fmt.Fprintf(w, "sudo systemctl restart tailscaled || sudo systemctl start tailscaled\n")
				fmt.Fprintf(w, "rm -rf \"$TS_TMP_DIR\" \"/tmp/${TS_TGZ}\" \"/tmp/${TS_TGZ}.sha256\"\n")
			}
		} else {
			fmt.Fprintf(w, "# macOS: ensure Tailscale app is installed (https://tailscale.com/download)\n")
		}
		upCmd := "tailscale up"
		if tsKey != "" {
			upCmd += " --auth-key=" + tsKey
		} else {
			if tsCreateAuthKey {
				fmt.Fprintf(w, "# NOTE: In normal abc execution this key can be auto-created from TAILSCALE_API_KEY (ephemeral=%t reusable=%t expiry=%s).\n", tsKeyEphemeral, tsKeyReusable, tsKeyExpiry)
			}
			fmt.Fprintf(w, ": \"${TS_AUTH_KEY:?set TS_AUTH_KEY to a Tailscale auth key}\"\n")
			upCmd += " --auth-key=${TS_AUTH_KEY}"
		}
		if tsHostname != "" {
			upCmd += " --hostname=" + tsHostname
		}
		fmt.Fprintf(w, "sudo %s 2>&1 || %s 2>&1\n", upCmd, upCmd)
	} else {
		fmt.Fprintf(w, "# Tailscale: disabled (direct-join mode)\n")
		fmt.Fprintf(w, "# To enable: re-run with --tailscale --tailscale-auth-key=<key>\n")
	}
	fmt.Fprintln(w)
	if autoNomadAdvertise {
		fmt.Fprintf(w, "# Resolve Nomad advertise address from Tailscale\n")
		fmt.Fprintf(w, "NOMAD_ADVERTISE=$(tailscale ip -4 2>/dev/null | head -n1)\n")
		fmt.Fprintf(w, "if [ -z \"$NOMAD_ADVERTISE\" ]; then echo \"Could not determine Tailscale IPv4 address\"; exit 1; fi\n")
		fmt.Fprintf(w, "echo \"Using Tailscale IP for Nomad advertise: ${NOMAD_ADVERTISE}\"\n")
		fmt.Fprintln(w)
	}

	if packageInstallMethod == packageInstallMethodPackageManager && goos == "linux" {
		// ── 2. Nomad install via package manager ─────────────────────────────────
		fmt.Fprintf(w, "# ── 2. Install Nomad via package manager ───────────────────────────────────\n")
		fmt.Fprintf(w, "if command -v apt-get >/dev/null 2>&1; then\n")
		fmt.Fprintf(w, "  sudo install -m 0755 -d /etc/apt/keyrings\n")
		fmt.Fprintf(w, "  curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/hashicorp.gpg\n")
		fmt.Fprintf(w, "  sudo chmod a+r /etc/apt/keyrings/hashicorp.gpg\n")
		fmt.Fprintf(w, "  CODENAME=$(. /etc/os-release && echo ${VERSION_CODENAME:-${UBUNTU_CODENAME:-}})\n")
		fmt.Fprintf(w, "  if [ -z \"$CODENAME\" ] && command -v lsb_release >/dev/null 2>&1; then CODENAME=$(lsb_release -cs); fi\n")
		fmt.Fprintf(w, "  if [ -z \"$CODENAME\" ]; then echo \"Unable to determine distro codename for HashiCorp repo\"; exit 1; fi\n")
		fmt.Fprintf(w, "  echo \"deb [signed-by=/etc/apt/keyrings/hashicorp.gpg] https://apt.releases.hashicorp.com ${CODENAME} main\" | sudo tee /etc/apt/sources.list.d/hashicorp.list >/dev/null\n")
		fmt.Fprintf(w, "  sudo apt-get update\n")
		fmt.Fprintf(w, "  sudo apt-get install -y nomad\n")
		fmt.Fprintf(w, "elif command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; then\n")
		fmt.Fprintf(w, "  sudo tee /etc/yum.repos.d/hashicorp.repo >/dev/null <<'REPO'\n")
		fmt.Fprintf(w, "[hashicorp]\n")
		fmt.Fprintf(w, "name=HashiCorp Stable - $basearch\n")
		fmt.Fprintf(w, "baseurl=https://rpm.releases.hashicorp.com/RHEL/$releasever/$basearch/stable\n")
		fmt.Fprintf(w, "enabled=1\n")
		fmt.Fprintf(w, "gpgcheck=1\n")
		fmt.Fprintf(w, "gpgkey=https://rpm.releases.hashicorp.com/gpg\n")
		fmt.Fprintf(w, "REPO\n")
		fmt.Fprintf(w, "  if command -v dnf >/dev/null 2>&1; then sudo dnf -y install nomad; else sudo yum -y install nomad; fi\n")
		fmt.Fprintf(w, "else\n")
		fmt.Fprintf(w, "  echo \"No supported package manager found for Nomad package install\" >&2\n")
		fmt.Fprintf(w, "  exit 1\n")
		fmt.Fprintf(w, "fi\n")
		fmt.Fprintf(w, "sudo mkdir -p \"%s\" \"%s\"\n", cfgDir, dataDir)
		for _, dir := range hostVolumeDirs {
			fmt.Fprintf(w, "sudo mkdir -p %q\n", dir)
		}
		fmt.Fprintln(w)
	} else {
		// ── 2. Nomad download + verify ────────────────────────────────────────────
		fmt.Fprintf(w, "# ── 2. Download and verify Nomad %s ────────────────────────────────────────\n", version)
		fmt.Fprintf(w, "NOMAD_VERSION=%s\n", version)
		fmt.Fprintf(w, "NOMAD_OS=%s\n", releaseOS)
		fmt.Fprintf(w, "NOMAD_ARCH=%s\n", releaseArch)
		fmt.Fprintf(w, "NOMAD_ZIP=\"nomad_${NOMAD_VERSION}_${NOMAD_OS}_${NOMAD_ARCH}.zip\"\n")
		fmt.Fprintf(w, "NOMAD_SHA=\"nomad_${NOMAD_VERSION}_SHA256SUMS\"\n")
		fmt.Fprintf(w, "NOMAD_BASE=\"%s/${NOMAD_VERSION}\"\n", nomadReleasesBase)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "curl -fsSL \"${NOMAD_BASE}/${NOMAD_ZIP}\" -o \"/tmp/${NOMAD_ZIP}\"\n")
		fmt.Fprintf(w, "curl -fsSL \"${NOMAD_BASE}/${NOMAD_SHA}\" -o \"/tmp/${NOMAD_SHA}\"\n")
		if goos == "darwin" {
			fmt.Fprintf(w, "# macOS: use shasum -a 256; install coreutils for sha256sum if preferred\n")
			fmt.Fprintf(w, "grep \"${NOMAD_ZIP}\" \"/tmp/${NOMAD_SHA}\" | awk '{print $1\"  /tmp/\"$2}' | shasum -a 256 --check\n")
		} else {
			fmt.Fprintf(w, "sha256sum --check --ignore-missing \"/tmp/${NOMAD_SHA}\"\n")
		}
		fmt.Fprintln(w)

		// ── 3. Install Nomad binary ───────────────────────────────────────────────
		fmt.Fprintf(w, "# ── 3. Install Nomad binary ─────────────────────────────────────────────────\n")
		fmt.Fprintf(w, "sudo mkdir -p \"%s\" \"%s\"\n", cfgDir, dataDir)
		for _, dir := range hostVolumeDirs {
			fmt.Fprintf(w, "sudo mkdir -p %q\n", dir)
		}
		fmt.Fprintf(w, "unzip -o \"/tmp/${NOMAD_ZIP}\" nomad -d /tmp/\n")
		fmt.Fprintf(w, "sudo mv /tmp/nomad \"%s\"\n", binPath)
		fmt.Fprintf(w, "sudo chmod 755 \"%s\"\n", binPath)
		fmt.Fprintf(w, "sudo chown root:root \"%s\"\n", binPath)
		fmt.Fprintf(w, "rm \"/tmp/${NOMAD_ZIP}\" \"/tmp/${NOMAD_SHA}\"\n")
		fmt.Fprintln(w)
	}

	// ── 4. Nomad config ───────────────────────────────────────────────────────
	fmt.Fprintf(w, "# ── 4. Nomad config (%s) ─────────────────────────────────────────\n", cfgPath)
	if autoNomadAdvertise {
		fmt.Fprintf(w, "sudo tee \"%s\" > /dev/null <<HCL\n", cfgPath)
	} else {
		fmt.Fprintf(w, "sudo tee \"%s\" > /dev/null <<'HCL'\n", cfgPath)
	}
	fmt.Fprint(w, hclContent)
	fmt.Fprintf(w, "HCL\n")
	fmt.Fprintf(w, "sudo chown root:root \"%s\"\n", cfgPath)
	fmt.Fprintf(w, "sudo chmod 640 \"%s\"\n", cfgPath)
	fmt.Fprintln(w)

	// ── 5. Service registration ────────────────────────────────────────────────
	fmt.Fprintf(w, "# ── 5. Service registration ──────────────────────────────────────────────────\n")
	switch goos {
	case "linux":
		if packageInstallMethod == packageInstallMethodPackageManager {
			fmt.Fprintf(w, "sudo systemctl daemon-reload\n")
		} else {
			fmt.Fprintf(w, "sudo tee /etc/systemd/system/nomad.service > /dev/null <<'UNIT'\n")
			fmt.Fprint(w, nomadSystemdUnit)
			fmt.Fprintf(w, "UNIT\n")
			fmt.Fprintf(w, "sudo chown root:root /etc/systemd/system/nomad.service\n")
			fmt.Fprintf(w, "sudo systemctl daemon-reload\n")
		}
		if !skipEnable {
			fmt.Fprintf(w, "sudo systemctl enable nomad\n")
		}
		if !skipStart {
			if packageInstallMethod == packageInstallMethodPackageManager {
				fmt.Fprintf(w, "sudo systemctl restart nomad || sudo systemctl start nomad\n")
			} else {
				fmt.Fprintf(w, "sudo systemctl start nomad\n")
			}
		}
	case "darwin":
		const plistPath = "/Library/LaunchDaemons/nomad.plist"
		fmt.Fprintf(w, "sudo tee \"%s\" > /dev/null <<'PLIST'\n", plistPath)
		fmt.Fprint(w, nomadLaunchdPlist)
		fmt.Fprintf(w, "PLIST\n")
		fmt.Fprintf(w, "sudo chown root:wheel \"%s\"\n", plistPath)
		if !skipEnable && !skipStart {
			fmt.Fprintf(w, "sudo launchctl load -w \"%s\"\n", plistPath)
		}
	}
	fmt.Fprintln(w)

	// ── 6. Health check ───────────────────────────────────────────────────────
	if !skipStart {
		fmt.Fprintf(w, "# ── 6. Verify ───────────────────────────────────────────────────────────────\n")
		fmt.Fprintf(w, "echo 'Waiting for Nomad agent...'\n")
		fmt.Fprintf(w, "for i in $(seq 1 20); do\n")
		fmt.Fprintf(w, "  curl -sf http://127.0.0.1:4646/v1/agent/self > /dev/null 2>&1 && echo '✓ Nomad agent healthy' && break\n")
		fmt.Fprintf(w, "  echo \"  attempt $i/20 — retrying in 3s...\"\n")
		fmt.Fprintf(w, "  sleep 3\n")
		fmt.Fprintf(w, "done\n")
	}

	return nil
}

// printWindowsScript emits PowerShell-style comments and commands for Windows.
// Full automation is not supported on Windows; the output is a documented guide.
func printWindowsScript(w io.Writer, version, releaseOS, releaseArch, binPath, cfgPath, dataDir string, hostVolumeDirs []string, hclContent string, useTailscale bool, tsKey, tsHostname string, skipEnable, skipStart bool) {
	fmt.Fprintf(w, "# ── 1. Tailscale ────────────────────────────────────────────────────────────\n")
	if useTailscale {
		fmt.Fprintf(w, "# Ensure Tailscale is installed: https://tailscale.com/download/windows\n")
		upCmd := "tailscale up"
		if tsKey != "" {
			upCmd += " --auth-key=" + tsKey
		} else {
			upCmd += " --auth-key=<YOUR_TAILSCALE_AUTH_KEY>"
		}
		if tsHostname != "" {
			upCmd += " --hostname=" + tsHostname
		}
		fmt.Fprintf(w, "# Run in PowerShell (as Administrator):\n")
		fmt.Fprintf(w, "#   %s\n", upCmd)
	} else {
		fmt.Fprintf(w, "# Tailscale: disabled (direct-join mode)\n")
	}
	fmt.Fprintln(w)

	zipName := fmt.Sprintf("nomad_%s_%s_%s.zip", version, releaseOS, releaseArch)
	baseURL := fmt.Sprintf("%s/%s", nomadReleasesBase, version)
	fmt.Fprintf(w, "# ── 2. Download and verify Nomad %s ────────────────────────────────────────\n", version)
	fmt.Fprintf(w, "# In PowerShell (as Administrator):\n")
	fmt.Fprintf(w, "#\n")
	fmt.Fprintf(w, "#   $ver = \"%s\"\n", version)
	fmt.Fprintf(w, "#   Invoke-WebRequest -Uri \"%s/%s\" -OutFile \"$env:TEMP\\nomad.zip\"\n", baseURL, zipName)
	fmt.Fprintf(w, "#   Invoke-WebRequest -Uri \"%s/nomad_%s_SHA256SUMS\" -OutFile \"$env:TEMP\\nomad_SHA256SUMS\"\n", baseURL, version)
	fmt.Fprintf(w, "#   # Verify checksum (manual): compare Get-FileHash output with SHA256SUMS contents\n")
	fmt.Fprintf(w, "#   Get-FileHash \"$env:TEMP\\nomad.zip\" -Algorithm SHA256\n")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "# ── 3. Install binary ───────────────────────────────────────────────────────\n")
	binDir := binPath[:strings.LastIndex(binPath, "\\")]
	fmt.Fprintf(w, "#   New-Item -ItemType Directory -Force -Path \"%s\"\n", binDir)
	fmt.Fprintf(w, "#   New-Item -ItemType Directory -Force -Path \"%s\"\n", dataDir)
	for _, dir := range hostVolumeDirs {
		fmt.Fprintf(w, "#   New-Item -ItemType Directory -Force -Path \"%s\"\n", dir)
	}
	fmt.Fprintf(w, "#   Expand-Archive -Path \"$env:TEMP\\nomad.zip\" -DestinationPath \"$env:TEMP\\nomad_extracted\" -Force\n")
	fmt.Fprintf(w, "#   Move-Item \"$env:TEMP\\nomad_extracted\\nomad.exe\" \"%s\" -Force\n", binPath)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "# ── 4. Nomad config (%s)\n", cfgPath)
	cfgDir := cfgPath[:strings.LastIndex(cfgPath, "\\")]
	fmt.Fprintf(w, "#   New-Item -ItemType Directory -Force -Path \"%s\"\n", cfgDir)
	fmt.Fprintf(w, "#   @'\n")
	for _, line := range strings.Split(strings.TrimRight(hclContent, "\n"), "\n") {
		fmt.Fprintf(w, "#   %s\n", line)
	}
	fmt.Fprintf(w, "#   '@ | Set-Content -Path \"%s\" -Encoding UTF8\n", cfgPath)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "# ── 5. Service registration ─────────────────────────────────────────────────\n")
	fmt.Fprintf(w, "# Windows Service registration (run as Administrator):\n")
	fmt.Fprintf(w, "#   sc.exe create nomad binPath= \"\\\"%s\\\" agent -config \\\"%s\\\"\"\n", binPath, cfgPath)
	if !skipStart {
		fmt.Fprintf(w, "#   sc.exe start nomad\n")
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "# ── 6. Verify ───────────────────────────────────────────────────────────────\n")
	fmt.Fprintf(w, "# After starting Nomad:\n")
	fmt.Fprintf(w, "#   Invoke-RestMethod http://127.0.0.1:4646/v1/agent/self\n")
}

// parseTargetOS parses a "os/arch" string (e.g. "linux/amd64") into goos and goarch.
// Defaults to linux/amd64 if the string is empty.
func parseTargetOS(s string) (goos, goarch string, err error) {
	if s == "" {
		return "linux", "amd64", nil
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("--target-os must be in os/arch format (e.g. linux/amd64, darwin/arm64), got %q", s)
	}
	goos = strings.ToLower(parts[0])
	goarch = strings.ToLower(parts[1])
	switch goos {
	case "linux", "darwin", "windows":
	default:
		return "", "", fmt.Errorf("unsupported OS %q in --target-os (supported: linux, darwin, windows)", goos)
	}
	switch goarch {
	case "amd64", "arm64", "386", "arm":
	default:
		return "", "", fmt.Errorf("unsupported arch %q in --target-os (supported: amd64, arm64, 386, arm)", goarch)
	}
	return goos, goarch, nil
}

// localOSArch returns the current machine's OS and arch as Go runtime strings.
func localOSArch() (string, string) {
	return runtime.GOOS, runtime.GOARCH
}
