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
	hclContent := GenerateClientHCL(cfg.NodeConfig)

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
		printWindowsScript(w, version, releaseOS, releaseArch, binPath, cfgPath, dataDir, hclContent, useTailscale, tsKey, tsHostname, skipEnable, skipStart)
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
			fmt.Fprintf(w, "curl -fsSL https://tailscale.com/install.sh | sudo sh\n")
		} else {
			fmt.Fprintf(w, "# macOS: ensure Tailscale app is installed (https://tailscale.com/download)\n")
		}
		upCmd := "tailscale up"
		if tsKey != "" {
			upCmd += " --auth-key=" + tsKey
		} else {
			upCmd += " --auth-key=<YOUR_TAILSCALE_AUTH_KEY>"
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
	fmt.Fprintf(w, "unzip -o \"/tmp/${NOMAD_ZIP}\" nomad -d /tmp/\n")
	fmt.Fprintf(w, "sudo mv /tmp/nomad \"%s\"\n", binPath)
	fmt.Fprintf(w, "sudo chmod 755 \"%s\"\n", binPath)
	fmt.Fprintf(w, "sudo chown root:root \"%s\"\n", binPath)
	fmt.Fprintf(w, "rm \"/tmp/${NOMAD_ZIP}\" \"/tmp/${NOMAD_SHA}\"\n")
	fmt.Fprintln(w)

	// ── 4. Nomad config ───────────────────────────────────────────────────────
	fmt.Fprintf(w, "# ── 4. Nomad config (%s) ─────────────────────────────────────────\n", cfgPath)
	fmt.Fprintf(w, "sudo tee \"%s\" > /dev/null <<'HCL'\n", cfgPath)
	fmt.Fprint(w, hclContent)
	fmt.Fprintf(w, "HCL\n")
	fmt.Fprintf(w, "sudo chown root:root \"%s\"\n", cfgPath)
	fmt.Fprintf(w, "sudo chmod 640 \"%s\"\n", cfgPath)
	fmt.Fprintln(w)

	// ── 5. Service registration ────────────────────────────────────────────────
	fmt.Fprintf(w, "# ── 5. Service registration ──────────────────────────────────────────────────\n")
	switch goos {
	case "linux":
		fmt.Fprintf(w, "sudo tee /etc/systemd/system/nomad.service > /dev/null <<'UNIT'\n")
		fmt.Fprint(w, nomadSystemdUnit)
		fmt.Fprintf(w, "UNIT\n")
		fmt.Fprintf(w, "sudo chown root:root /etc/systemd/system/nomad.service\n")
		fmt.Fprintf(w, "sudo systemctl daemon-reload\n")
		if !skipEnable {
			fmt.Fprintf(w, "sudo systemctl enable nomad\n")
		}
		if !skipStart {
			fmt.Fprintf(w, "sudo systemctl start nomad\n")
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
func printWindowsScript(w io.Writer, version, releaseOS, releaseArch, binPath, cfgPath, dataDir, hclContent string, useTailscale bool, tsKey, tsHostname string, skipEnable, skipStart bool) {
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
