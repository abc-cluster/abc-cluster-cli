package node

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	nomadReleasesBase   = "https://releases.hashicorp.com/nomad"
	defaultNomadVersion = "1.11.3"

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
	Version       string
	InstallMethod string
	NodeConfig    NodeConfig
	SkipEnable    bool
	SkipStart     bool
}

// InstallNomad downloads, verifies (SHA256), installs, and starts Nomad on the target.
// Implements the hashi-up install pattern: download → checksum → unzip → service.
func InstallNomad(ctx context.Context, ex Executor, cfg NomadInstallConfig, w io.Writer) error {

	goos, goarch := ex.OS(), ex.Arch()
	installMethod, err := normalizePackageInstallMethod(cfg.InstallMethod)
	if err != nil {
		return err
	}

	if installMethod == packageInstallMethodPackageManager && goos != "linux" {
		fmt.Fprintf(w, "    ! Package-manager install is not yet supported on %s; falling back to static binary install\n", goos)
		installMethod = packageInstallMethodStatic
	}

	if installMethod == packageInstallMethodPackageManager {
		fmt.Fprintf(w, "\n  Installing Nomad via package manager...\n")
		if cfg.Version != "" {
			fmt.Fprintf(w, "    ! --nomad-version is ignored with --package-install-method=package-manager (repo version will be installed)\n")
		}
		if err := installNomadPackageManagerLinux(ctx, ex, w); err != nil {
			return err
		}

		_, cfgDir, cfgPath, dataDir := nomadPaths(goos)
		if err := ex.Run(ctx, fmt.Sprintf("sudo mkdir -p %s %s", cfgDir, dataDir), io.Discard); err != nil {
			return fmt.Errorf("create directories: %w", err)
		}
		if err := createHostVolumeDirectories(ctx, ex, goos, cfg.NodeConfig.HostVolumes); err != nil {
			return err
		}

		cfg.NodeConfig.DataDir = dataDir
		hclContent := GenerateClientHCL(cfg.NodeConfig)
		if err := ex.Upload(ctx, strings.NewReader(hclContent), cfgPath, 0640); err != nil {
			return fmt.Errorf("upload config to %s: %w", cfgPath, err)
		}
		_ = ex.Run(ctx, fmt.Sprintf("sudo chown root:root %s && sudo chmod 640 %s", cfgPath, cfgPath), io.Discard)
		fmt.Fprintf(w, "    ✓ Config written to %s\n", cfgPath)

		return registerPackageManagerNomadService(ctx, ex, goos, cfg.SkipEnable, cfg.SkipStart, w)
	}

	version := cfg.Version
	if version == "" {
		version, err = latestNomadVersion(ctx)
		if err != nil {
			version = defaultNomadVersion
			fmt.Fprintf(w, "    ! Could not fetch latest version (%v), using %s\n", err, version)
		}
	}

	fmt.Fprintf(w, "\n  Installing Nomad %s...\n", version)

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
	if err := createHostVolumeDirectories(ctx, ex, goos, cfg.NodeConfig.HostVolumes); err != nil {
		return err
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

func ApplyNomadConfig(ctx context.Context, ex Executor, cfg NomadInstallConfig, w io.Writer) error {
	goos := ex.OS()
	_, cfgDir, cfgPath, dataDir := nomadPaths(goos)

	var mkdirCmd string
	switch goos {
	case "windows":
		mkdirCmd = fmt.Sprintf("mkdir \"%s\" \"%s\"", cfgDir, dataDir)
	default:
		mkdirCmd = fmt.Sprintf("sudo mkdir -p %s %s", cfgDir, dataDir)
	}
	if err := ex.Run(ctx, mkdirCmd, io.Discard); err != nil {
		return fmt.Errorf("create Nomad config directories: %w", err)
	}
	if err := createHostVolumeDirectories(ctx, ex, goos, cfg.NodeConfig.HostVolumes); err != nil {
		return err
	}

	cfg.NodeConfig.DataDir = dataDir
	hclContent := GenerateClientHCL(cfg.NodeConfig)

	mode := os.FileMode(0640)
	if goos == "windows" {
		mode = 0644
	}
	if err := ex.Upload(ctx, strings.NewReader(hclContent), cfgPath, mode); err != nil {
		return fmt.Errorf("upload config to %s: %w", cfgPath, err)
	}
	if goos != "windows" {
		_ = ex.Run(ctx, fmt.Sprintf("sudo chown root:root %s && sudo chmod 640 %s", cfgPath, cfgPath), io.Discard)
	}
	fmt.Fprintf(w, "    ✓ Config written to %s\n", cfgPath)

	if cfg.SkipStart {
		fmt.Fprintf(w, "    - Nomad restart skipped per --skip-start\n")
		return nil
	}

	switch goos {
	case "linux":
		if err := ex.Run(ctx, "sudo systemctl restart nomad || sudo systemctl start nomad", io.Discard); err != nil {
			return fmt.Errorf("restart nomad service: %w", err)
		}
		fmt.Fprintf(w, "    ✓ Nomad service restarted\n")
	case "darwin":
		_ = ex.Run(ctx, "sudo launchctl kickstart -k system/nomad || true", io.Discard)
		fmt.Fprintf(w, "    ✓ Nomad restart requested via launchctl\n")
	case "windows":
		fmt.Fprintf(w, "    ! Restart Nomad service manually on Windows to apply changes\n")
	}
	return nil
}

func installNomadPackageManagerLinux(ctx context.Context, ex Executor, w io.Writer) error {
	pkgMgr := detectPkgManager(ctx, ex)
	switch pkgMgr {
	case "apt":
		fmt.Fprintf(w, "    Installing via apt...\n")
		cmd := strings.Join([]string{
			"sudo install -m 0755 -d /etc/apt/keyrings",
			"curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/hashicorp.gpg",
			"sudo chmod a+r /etc/apt/keyrings/hashicorp.gpg",
			"CODENAME=$(. /etc/os-release && echo ${VERSION_CODENAME:-${UBUNTU_CODENAME:-}})",
			"if [ -z \"$CODENAME\" ] && command -v lsb_release >/dev/null 2>&1; then CODENAME=$(lsb_release -cs); fi",
			"if [ -z \"$CODENAME\" ]; then echo \"unable to determine distro codename for HashiCorp repo\" >&2; exit 1; fi",
			"echo \"deb [signed-by=/etc/apt/keyrings/hashicorp.gpg] https://apt.releases.hashicorp.com ${CODENAME} main\" | sudo tee /etc/apt/sources.list.d/hashicorp.list >/dev/null",
			"sudo apt-get update",
			"sudo apt-get install -y nomad",
		}, " && ")
		if err := ex.Run(ctx, cmd, io.Discard); err != nil {
			return fmt.Errorf("install nomad with apt: %w", err)
		}
	case "dnf", "yum":
		fmt.Fprintf(w, "    Installing via %s...\n", pkgMgr)
		cmd := strings.Join([]string{
			"printf '%s\n' '[hashicorp]' 'name=HashiCorp Stable - $basearch' 'baseurl=https://rpm.releases.hashicorp.com/RHEL/$releasever/$basearch/stable' 'enabled=1' 'gpgcheck=1' 'gpgkey=https://rpm.releases.hashicorp.com/gpg' | sudo tee /etc/yum.repos.d/hashicorp.repo >/dev/null",
			"if command -v dnf >/dev/null 2>&1; then sudo dnf -y install nomad; else sudo yum -y install nomad; fi",
		}, " && ")
		if err := ex.Run(ctx, cmd, io.Discard); err != nil {
			return fmt.Errorf("install nomad with %s: %w", pkgMgr, err)
		}
	default:
		return fmt.Errorf("no supported package manager detected for Nomad package installation")
	}
	return nil
}

func registerPackageManagerNomadService(ctx context.Context, ex Executor, goos string, skipEnable, skipStart bool, w io.Writer) error {
	if goos != "linux" {
		return registerService(ctx, ex, goos, skipEnable, skipStart, w)
	}
	_ = ex.Run(ctx, "sudo systemctl daemon-reload", io.Discard)
	if !skipEnable {
		if err := ex.Run(ctx, "sudo systemctl enable nomad", io.Discard); err != nil {
			return fmt.Errorf("systemctl enable nomad: %w", err)
		}
	}
	if !skipStart {
		if err := ex.Run(ctx, "sudo systemctl restart nomad || sudo systemctl start nomad", io.Discard); err != nil {
			return fmt.Errorf("systemctl start nomad: %w", err)
		}
		fmt.Fprintf(w, "    ✓ systemd service enabled and started\n")
	} else {
		fmt.Fprintf(w, "    ✓ package-managed Nomad installed (start skipped per --skip-start)\n")
	}
	return nil
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

func createHostVolumeDirectories(ctx context.Context, ex Executor, goos string, hostVolumes []NomadHostVolume) error {
	for _, path := range hostVolumePaths(hostVolumes) {
		var cmd string
		switch goos {
		case "windows":
			cmd = fmt.Sprintf("mkdir %q", path)
		default:
			cmd = fmt.Sprintf("sudo mkdir -p %q", path)
		}
		if err := ex.Run(ctx, cmd, io.Discard); err != nil {
			return fmt.Errorf("create host volume directory %s: %w", path, err)
		}
	}
	return nil
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────
const (
	artifactFetchUserAgent       = "abc-cluster-cli-artifact-fetcher/1.0"
	artifactFetchRetryAttempts   = 4
	artifactFetchBaseRetryDelay  = 500 * time.Millisecond
	artifactFetchMaxRetryBackoff = 8 * time.Second
)

var nomadHTTPClient = &http.Client{Timeout: 10 * time.Minute}

func fetchBytes(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= artifactFetchRetryAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", artifactFetchUserAgent)
		req.Header.Set("Accept", "*/*")

		resp, err := nomadHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < artifactFetchRetryAttempts {
				if err := waitForArtifactRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("request %s failed after %d attempts: %w", url, attempt, lastErr)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read response body for %s: %w", url, readErr)
			if attempt < artifactFetchRetryAttempts {
				if err := waitForArtifactRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode == http.StatusOK {
			return body, nil
		}

		lastErr = fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
		if !isRetryableFetchStatus(resp.StatusCode) || attempt == artifactFetchRetryAttempts {
			return nil, lastErr
		}
		if err := waitForArtifactRetry(ctx, attempt); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func fetchText(ctx context.Context, url string) (string, error) {
	b, err := fetchBytes(ctx, url)
	return string(b), err
}

func isRetryableFetchStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitForArtifactRetry(ctx context.Context, attempt int) error {
	delay := artifactFetchBaseRetryDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= artifactFetchMaxRetryBackoff {
			delay = artifactFetchMaxRetryBackoff
			break
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// latestNomadVersion queries releases.hashicorp.com for the latest stable Nomad version.
func latestNomadVersion(ctx context.Context) (string, error) {
	data, err := fetchBytes(ctx, nomadReleasesBase+"/index.json")
	if err != nil {
		return "", err
	}

	var idx struct {
		Versions map[string]json.RawMessage `json:"versions"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return "", fmt.Errorf("parse index.json: %w", err)
	}
	latest := ""
	var latestParts [3]int
	for v := range idx.Versions {
		parts, ok := parseStableSemver(v)
		if !ok {
			continue
		}
		if latest == "" || compareSemverParts(parts, latestParts) > 0 {
			latest = v
			latestParts = parts
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no stable version found in index")
	}
	return latest, nil
}

func parseStableSemver(v string) ([3]int, bool) {
	var out [3]int
	if strings.HasPrefix(v, "v") {
		v = strings.TrimPrefix(v, "v")
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func compareSemverParts(a, b [3]int) int {
	for i := 0; i < len(a); i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
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
