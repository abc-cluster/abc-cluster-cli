package node

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const tailscalePackagesBase = "https://pkgs.tailscale.com/stable"

const tailscaledSystemdUnit = `[Unit]
Description=Tailscale node agent
Documentation=https://tailscale.com/kb/
Wants=network-online.target
After=network-online.target

[Service]
Type=notify
ExecStart=/usr/local/bin/tailscaled --state=/var/lib/tailscale/tailscaled.state
Restart=on-failure
RestartSec=2
RuntimeDirectory=tailscale
RuntimeDirectoryMode=0755

[Install]
WantedBy=multi-user.target
`

// InstallTailscale installs Tailscale on the target (Linux only — macOS/Windows
// assume the GUI app is present) and runs `tailscale up` to join the tailnet.
//
// Default Linux install path is static binaries. Package-manager install remains
// available via --package-install-method=package-manager.
func InstallTailscale(ctx context.Context, ex Executor, key, hostname, installMethod string, w io.Writer) error {
	method, err := normalizePackageInstallMethod(installMethod)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n  Installing Tailscale...\n")

	if ex.OS() == "linux" {
		switch method {
		case packageInstallMethodStatic:
			if err := installTailscaleStaticLinux(ctx, ex, w); err != nil {
				return err
			}
		case packageInstallMethodPackageManager:
			if err := installTailscalePackageManagerLinux(ctx, ex, w); err != nil {
				return err
			}
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

func installTailscalePackageManagerLinux(ctx context.Context, ex Executor, w io.Writer) error {
	fmt.Fprintf(w, "    Installing via package manager script...\n")
	script := "curl -fsSL https://tailscale.com/install.sh | sudo sh"
	if err := ex.Run(ctx, script, LineWriter(w, "      ")); err != nil {
		return fmt.Errorf("tailscale install script: %w", err)
	}
	return nil
}

func installTailscaleStaticLinux(ctx context.Context, ex Executor, w io.Writer) error {
	tarballName, version, err := latestTailscaleStaticTarball(ctx, ex.Arch())
	if err != nil {
		return fmt.Errorf("resolve tailscale static binary: %w", err)
	}
	if version == "" {
		version = "latest"
	}

	tarballURL := fmt.Sprintf("%s/%s", tailscalePackagesBase, tarballName)
	checksumURL := tarballURL + ".sha256"

	fmt.Fprintf(w, "    Downloading %s...\n", tarballName)
	tarball, err := fetchBytes(ctx, tarballURL)
	if err != nil {
		return fmt.Errorf("download %s: %w", tarballName, err)
	}

	expectedSHA, err := fetchText(ctx, checksumURL)
	if err != nil {
		return fmt.Errorf("download %s.sha256: %w", tarballName, err)
	}
	expectedSHA = strings.TrimSpace(expectedSHA)
	if expectedSHA == "" {
		return fmt.Errorf("empty checksum for %s", tarballName)
	}

	actual := sha256.Sum256(tarball)
	actualHex := hex.EncodeToString(actual[:])
	if !strings.EqualFold(actualHex, expectedSHA) {
		return fmt.Errorf("tailscale checksum mismatch for %s: got %s want %s", tarballName, actualHex, expectedSHA)
	}
	fmt.Fprintf(w, "    ✓ Tailscale checksum verified\n")

	tailscaleBin, err := extractFileFromTarGz(tarball, "tailscale")
	if err != nil {
		return fmt.Errorf("extract tailscale binary: %w", err)
	}
	tailscaledBin, err := extractFileFromTarGz(tarball, "tailscaled")
	if err != nil {
		return fmt.Errorf("extract tailscaled binary: %w", err)
	}

	if err := ex.Run(ctx, "sudo mkdir -p /usr/local/bin /var/lib/tailscale", io.Discard); err != nil {
		return fmt.Errorf("create tailscale directories: %w", err)
	}
	if err := ex.Upload(ctx, bytes.NewReader(tailscaleBin), "/usr/local/bin/tailscale", 0755); err != nil {
		return fmt.Errorf("upload tailscale binary: %w", err)
	}
	if err := ex.Upload(ctx, bytes.NewReader(tailscaledBin), "/usr/local/bin/tailscaled", 0755); err != nil {
		return fmt.Errorf("upload tailscaled binary: %w", err)
	}
	if err := ex.Run(ctx, "sudo chown root:root /usr/local/bin/tailscale /usr/local/bin/tailscaled && sudo chmod 755 /usr/local/bin/tailscale /usr/local/bin/tailscaled", io.Discard); err != nil {
		return fmt.Errorf("set tailscale binary permissions: %w", err)
	}
	fmt.Fprintf(w, "    ✓ Installed tailscale/tailscaled %s to /usr/local/bin\n", version)

	if err := ex.Upload(ctx, strings.NewReader(tailscaledSystemdUnit), "/etc/systemd/system/tailscaled.service", 0644); err != nil {
		return fmt.Errorf("upload tailscaled systemd unit: %w", err)
	}
	_ = ex.Run(ctx, "sudo chown root:root /etc/systemd/system/tailscaled.service", io.Discard)
	if err := ex.Run(ctx, "sudo systemctl daemon-reload && sudo systemctl enable tailscaled && (sudo systemctl restart tailscaled || sudo systemctl start tailscaled)", io.Discard); err != nil {
		return fmt.Errorf("start tailscaled service: %w", err)
	}
	fmt.Fprintf(w, "    ✓ tailscaled service enabled and started\n")
	return nil
}

func latestTailscaleStaticTarball(ctx context.Context, goArch string) (filename, version string, err error) {
	tailscaleArch := map[string]string{
		"amd64": "amd64",
		"arm64": "arm64",
		"arm":   "arm",
		"386":   "386",
	}[goArch]
	if tailscaleArch == "" {
		return "", "", fmt.Errorf("unsupported arch for tailscale static binaries: %s", goArch)
	}

	data, err := fetchBytes(ctx, tailscalePackagesBase+"/?mode=json")
	if err != nil {
		return "", "", err
	}

	var idx struct {
		Tarballs        map[string]string `json:"Tarballs"`
		TarballsVersion string            `json:"TarballsVersion"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return "", "", fmt.Errorf("parse tailscale package index: %w", err)
	}

	filename = idx.Tarballs[tailscaleArch]
	if filename == "" {
		return "", "", fmt.Errorf("no tailscale static tarball found for arch %s", tailscaleArch)
	}
	version = idx.TarballsVersion
	return filename, version, nil
}

func extractFileFromTarGz(tgzData []byte, filename string) ([]byte, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(tgzData))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gzReader.Close()

	tr := tar.NewReader(gzReader)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if path.Base(hdr.Name) != filename {
			continue
		}
		return io.ReadAll(tr)
	}
	return nil, fmt.Errorf("%q not found in tailscale archive", filename)
}
