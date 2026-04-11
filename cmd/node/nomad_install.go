package node

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	nomadReleasesBase   = "https://releases.hashicorp.com/nomad"
	defaultNomadVersion = "1.9.4"

	nomadSystemdUnit = `[Unit]
Description=Nomad
Documentation=https://developer.hashicorp.com/nomad/docs
Wants=network-online.target
After=network-online.target

[Service]
ExecReload=/bin/kill -HUP $MAINPID
ExecStart=/usr/local/bin/nomad agent -config /etc/nomad.d
KillMode=process
KillSignal=SIGINT
LimitNOFILE=65536
LimitNPROC=infinity
Restart=on-failure
RestartSec=2
StartLimitBurst=3
StartLimitIntervalSec=10
TasksMax=infinity

[Install]
WantedBy=multi-user.target
`

	nomadLaunchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>nomad</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/nomad</string>
    <string>agent</string>
    <string>-config</string>
    <string>/etc/nomad.d</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/var/log/nomad.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/nomad-error.log</string>
</dict>
</plist>
`
)

// NomadInstallConfig holds all installation parameters.
type NomadInstallConfig struct {
	Version    string
	NodeConfig NodeConfig
	SkipEnable bool
	SkipStart  bool
}

// InstallNomad downloads, verifies (SHA256), installs, and starts Nomad on the target.
// Implements the hashi-up install pattern: download → checksum → unzip → service.
func InstallNomad(ctx context.Context, ex Executor, cfg NomadInstallConfig, w io.Writer) error {
	version := cfg.Version
	if version == "" {
		var err error
		version, err = latestNomadVersion(ctx)
		if err != nil {
			version = defaultNomadVersion
			fmt.Fprintf(w, "    ! Could not fetch latest version (%v), using %s\n", err, version)
		}
	}

	fmt.Fprintf(w, "\n  Installing Nomad %s...\n", version)

	goos, goarch := ex.OS(), ex.Arch()

	// Map Go runtime names → HashiCorp release archive naming
	releaseOS := map[string]string{"linux": "linux", "darwin": "darwin", "windows": "windows"}[goos]
	if releaseOS == "" {
		return fmt.Errorf("unsupported OS: %s", goos)
	}
	releaseArch := map[string]string{
		"amd64": "amd64", "arm64": "arm64", "386": "386", "arm": "arm",
	}[goarch]
	if releaseArch == "" {
		return fmt.Errorf("unsupported arch: %s", goarch)
	}

	zipName := fmt.Sprintf("nomad_%s_%s_%s.zip", version, releaseOS, releaseArch)
	shaName := fmt.Sprintf("nomad_%s_SHA256SUMS", version)
	baseURL := fmt.Sprintf("%s/%s", nomadReleasesBase, version)

	// Fetch SHA256SUMS file and extract expected checksum (hashi-up pattern)
	shaURL := fmt.Sprintf("%s/%s", baseURL, shaName)
	sums, err := fetchText(ctx, shaURL)
	if err != nil {
		return fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	expectedSHA := extractSHA(sums, zipName)
	if expectedSHA == "" {
		return fmt.Errorf("SHA256 for %s not found in checksum file", zipName)
	}

	// Download archive
	zipURL := fmt.Sprintf("%s/%s", baseURL, zipName)
	fmt.Fprintf(w, "    Downloading %s...\n", zipName)
	zipData, err := fetchBytes(ctx, zipURL)
	if err != nil {
		return fmt.Errorf("download %s: %w", zipName, err)
	}

	// Verify SHA256 — abort on mismatch
	actualSum := sha256.Sum256(zipData)
	actualHex := hex.EncodeToString(actualSum[:])
	if !strings.EqualFold(actualHex, expectedSHA) {
		return fmt.Errorf("SHA256 mismatch for %s\n  got:  %s\n  want: %s", zipName, actualHex, expectedSHA)
	}
	fmt.Fprintf(w, "    ✓ Checksum verified\n")

	// Extract Nomad binary from zip in memory
	binName := "nomad"
	if goos == "windows" {
		binName = "nomad.exe"
	}
	nomadBin, err := extractFromZip(zipData, binName)
	if err != nil {
		return fmt.Errorf("extract %s from zip: %w", binName, err)
	}

	// OS-specific paths
	binPath, cfgDir, cfgPath, dataDir := nomadPaths(goos)

	// Create directories
	mkdirCmd := fmt.Sprintf("mkdir -p %q %q", cfgDir, dataDir)
	if goos != "windows" {
		mkdirCmd = fmt.Sprintf("sudo mkdir -p %s %s", cfgDir, dataDir)
	}
	if err := ex.Run(ctx, mkdirCmd, io.Discard); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	// Upload binary (0755 — executable)
	if err := ex.Upload(ctx, bytes.NewReader(nomadBin), binPath, 0755); err != nil {
		return fmt.Errorf("upload binary to %s: %w", binPath, err)
	}
	// Fix ownership on Unix
	if goos != "windows" {
		_ = ex.Run(ctx, fmt.Sprintf("sudo chown root:root %s", binPath), io.Discard)
	}
	fmt.Fprintf(w, "    ✓ Extracted to %s\n", binPath)

	// Generate and upload Nomad client config HCL
	cfg.NodeConfig.DataDir = dataDir
	hclContent := GenerateClientHCL(cfg.NodeConfig)
	if err := ex.Upload(ctx, strings.NewReader(hclContent), cfgPath, 0640); err != nil {
		return fmt.Errorf("upload config to %s: %w", cfgPath, err)
	}
	if goos != "windows" {
		_ = ex.Run(ctx, fmt.Sprintf("sudo chown root:root %s && sudo chmod 640 %s", cfgPath, cfgPath), io.Discard)
	}
	fmt.Fprintf(w, "    ✓ Config written to %s\n", cfgPath)

	// Register and (optionally) start the service
	return registerService(ctx, ex, goos, cfg.SkipEnable, cfg.SkipStart, w)
}

// registerService writes the service unit and starts Nomad per OS.
func registerService(ctx context.Context, ex Executor, goos string, skipEnable, skipStart bool, w io.Writer) error {
	switch goos {
	case "linux":
		const unitPath = "/etc/systemd/system/nomad.service"
		if err := ex.Upload(ctx, strings.NewReader(nomadSystemdUnit), unitPath, 0644); err != nil {
			return fmt.Errorf("upload systemd unit: %w", err)
		}
		_ = ex.Run(ctx, "sudo chown root:root "+unitPath, io.Discard)
		_ = ex.Run(ctx, "sudo systemctl daemon-reload", io.Discard)
		if !skipEnable {
			if err := ex.Run(ctx, "sudo systemctl enable nomad", io.Discard); err != nil {
				return fmt.Errorf("systemctl enable nomad: %w", err)
			}
		}
		if !skipStart {
			if err := ex.Run(ctx, "sudo systemctl start nomad", io.Discard); err != nil {
				return fmt.Errorf("systemctl start nomad: %w", err)
			}
			fmt.Fprintf(w, "    ✓ systemd service enabled and started\n")
		} else {
			fmt.Fprintf(w, "    ✓ systemd service unit installed (start skipped per --skip-start)\n")
		}

	case "darwin":
		const plistPath = "/Library/LaunchDaemons/nomad.plist"
		if err := ex.Upload(ctx, strings.NewReader(nomadLaunchdPlist), plistPath, 0644); err != nil {
			return fmt.Errorf("upload launchd plist: %w", err)
		}
		_ = ex.Run(ctx, "sudo chown root:wheel "+plistPath, io.Discard)
		if !skipEnable && !skipStart {
			if err := ex.Run(ctx, "sudo launchctl load -w "+plistPath, io.Discard); err != nil {
				return fmt.Errorf("launchctl load: %w", err)
			}
			fmt.Fprintf(w, "    ✓ launchd plist written and service loaded\n")
		} else {
			fmt.Fprintf(w, "    ✓ launchd plist written to %s (load skipped)\n", plistPath)
		}

	case "windows":
		binPath, _, cfgPath, _ := nomadPaths("windows")
		fmt.Fprintf(w, "\n  Note: Automatic Windows Service registration is not yet supported.\n")
		fmt.Fprintf(w, "  To start Nomad manually, run:\n")
		fmt.Fprintf(w, "    \"%s\" agent -config \"%s\"\n", binPath, cfgPath)
		fmt.Fprintf(w, "  To register as a Windows Service:\n")
		fmt.Fprintf(w, "    sc.exe create nomad binPath= \"\\\"%s\\\" agent -config \\\"%s\\\"\"\n", binPath, cfgPath)
		fmt.Fprintf(w, "    sc.exe start nomad\n")
	}
	return nil
}

// nomadPaths returns OS-specific installation paths.
func nomadPaths(goos string) (binPath, cfgDir, cfgPath, dataDir string) {
	if goos == "windows" {
		binPath = `C:\Program Files\Nomad\nomad.exe`
		cfgDir = `C:\ProgramData\Nomad`
		cfgPath = `C:\ProgramData\Nomad\client.hcl`
		dataDir = `C:\ProgramData\Nomad\data`
	} else {
		binPath = "/usr/local/bin/nomad"
		cfgDir = "/etc/nomad.d"
		cfgPath = "/etc/nomad.d/client.hcl"
		dataDir = "/opt/nomad/data"
	}
	return
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

var nomadHTTPClient = &http.Client{Timeout: 10 * time.Minute}

func fetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := nomadHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func fetchText(ctx context.Context, url string) (string, error) {
	b, err := fetchBytes(ctx, url)
	return string(b), err
}

// latestNomadVersion queries releases.hashicorp.com for the latest stable Nomad version.
func latestNomadVersion(ctx context.Context) (string, error) {
	// The index.json approach is complex; use a simpler heuristic: fetch the
	// releases page and look for the first non-pre-release version.
	// For simplicity, return a known-good version if the HTTP call fails.
	data, err := fetchText(ctx, nomadReleasesBase+"/index.json")
	if err != nil {
		return "", err
	}
	// Quick scan for versions like "1.9.4" (non-alpha/beta/rc)
	// This avoids importing encoding/json for a large response.
	latest := ""
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"version": "`) {
			v := strings.TrimPrefix(line, `"version": "`)
			v = strings.TrimSuffix(v, `",`)
			v = strings.Trim(v, `"`)
			// Skip pre-releases (alpha, beta, rc)
			if strings.ContainsAny(v, "abcdefghijklmnopqrstuvwxyz") {
				continue
			}
			if latest == "" || versionGreater(v, latest) {
				latest = v
			}
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no stable version found in index")
	}
	return latest, nil
}

// versionGreater does a simple lexicographic comparison good enough for semver.
func versionGreater(a, b string) bool {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if aParts[i] > bParts[i] {
			return true
		}
		if aParts[i] < bParts[i] {
			return false
		}
	}
	return len(aParts) > len(bParts)
}

// extractSHA parses a HashiCorp-format SHA256SUMS file.
// Each line: "<sha256>  <filename>" or "<sha256> *<filename>"
func extractSHA(sums, filename string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			name := strings.TrimLeft(fields[1], "*")
			if name == filename {
				return fields[0]
			}
		}
	}
	return ""
}

// extractFromZip finds and reads a named file from an in-memory zip archive.
func extractFromZip(zipData []byte, filename string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range r.File {
		if f.Name == filename {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s in zip: %w", filename, err)
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%q not found in zip archive", filename)
}
