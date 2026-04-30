package utils

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	defaultManagedBinaryDir = ".abc/binaries"
	defaultAssetDir         = ".abc/assets"
)

type BinarySetupResult struct {
	Name      string
	Path      string
	Version   string
	Skipped   bool
	SkipCause string
}

func ManagedBinaryDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("ABC_BINARIES_DIR")); v != "" {
		if err := os.MkdirAll(v, 0o755); err != nil {
			return "", fmt.Errorf("create managed binary dir %q: %w", v, err)
		}
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, defaultManagedBinaryDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create managed binary dir %q: %w", dir, err)
	}
	return dir, nil
}

func ManagedBinaryPath(name string) (string, error) {
	dir, err := ManagedBinaryDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// AssetDir returns the directory for cross-arch distribution artifacts
// (~/.abc/assets/ by default, overridable via ABC_ASSETS_DIR).
//
// This directory holds arch-suffixed binaries (<tool>-<os>-<arch>), JARs, and
// tools.toml — everything that gets fetched for cluster-node distribution and
// pushed to S3. It is separate from ManagedBinaryDir() (~/.abc/binaries/),
// which holds only plain-named host-platform executables safe to add to $PATH.
func AssetDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("ABC_ASSETS_DIR")); v != "" {
		if err := os.MkdirAll(v, 0o755); err != nil {
			return "", fmt.Errorf("create asset dir %q: %w", v, err)
		}
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, defaultAssetDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create asset dir %q: %w", dir, err)
	}
	return dir, nil
}

// AssetPath returns the full path for a named file inside AssetDir().
func AssetPath(name string) (string, error) {
	dir, err := AssetDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func SetupNomadAndProbeBinaries(w io.Writer) ([]BinarySetupResult, error) {
	results := make([]BinarySetupResult, 0, 2)

	nomadRes, err := setupNomadBinary(w)
	if err != nil {
		return results, err
	}
	results = append(results, nomadRes)

	probeRes, err := SetupNodeProbeBinary(w)
	if err != nil {
		return results, err
	}
	results = append(results, probeRes)
	return results, nil
}

// SetupNodeProbeBinary downloads abc-node-probe from GitHub releases into the managed binary dir.
func SetupNodeProbeBinary(w io.Writer) (BinarySetupResult, error) {
	return setupNodeProbeBinary(w)
}

func SetupTailscaleBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "tailscale", "tailscale", "tailscale", []string{".tgz", ".tar.gz"}, "tar.gz")
}

func SetupEgetBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "eget", "zyedidia", "eget", []string{".tar.gz", ".tgz"}, "tar.gz")
}

func SetupHashiUpBinary(w io.Writer) (BinarySetupResult, error) {
	return setupHashiUpBinary(w)
}

func SetupTerraformBinary(w io.Writer) (BinarySetupResult, error) {
	return setupHashicorpBinaryViaHashiUp(w, "terraform", "terraform")
}

func SetupConsulBinary(w io.Writer) (BinarySetupResult, error) {
	return setupHashicorpBinaryViaHashiUp(w, "consul", "consul")
}

func SetupBoundaryBinary(w io.Writer) (BinarySetupResult, error) {
	return setupHashicorpBinaryViaHashiUp(w, "boundary", "boundary")
}

func SetupNomadPackBinary(w io.Writer) (BinarySetupResult, error) {
	return setupHashicorpBinaryViaHashiUp(w, "nomad-pack", "nomad-pack")
}

// SetupRcloneBinary downloads the latest rclone release from GitHub into the managed binary dir.
func SetupRcloneBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "rclone", "rclone", "rclone", []string{".zip"}, "zip")
}

func SetupVaultBinary(w io.Writer) (BinarySetupResult, error) {
	return setupHashicorpBinaryViaHashiUp(w, "vault", "vault")
}

func SetupGrafanaBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGrafanaBinary(w)
}

func setupGrafanaBinary(w io.Writer) (BinarySetupResult, error) {
	name := "grafana-cli"
	if existing := findBinaryInPath(name); existing != "" {
		fmt.Fprintf(w, "  - %s: found in PATH at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "path"}, nil
	}
	if existing, ok := findManagedBinary(name); ok {
		fmt.Fprintf(w, "  - %s: found in managed dir at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "managed"}, nil
	}

	release, err := FetchLatestReleaseAllowPrereleases("grafana", "grafana")
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("fetch %s release metadata: %w", name, err)
	}
	version := strings.TrimPrefix(release.TagName, "v")

	goos := normalizeGOOS(runtime.GOOS)
	goarch := normalizeGOARCH(runtime.GOARCH)
	assetName := fmt.Sprintf("grafana-%s.%s-%s.tar.gz", version, goos, goarch)
	if runtime.GOOS == "windows" {
		assetName = fmt.Sprintf("grafana-%s.windows-%s.zip", version, goarch)
	}
	downloadURL := fmt.Sprintf("https://dl.grafana.com/oss/release/%s", assetName)

	dest, err := ManagedBinaryPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}

	if runtime.GOOS == "windows" {
		if err := downloadAndExtractAsset(downloadURL, assetName, "zip", name, dest); err != nil {
			return BinarySetupResult{}, fmt.Errorf("install %s: %w", name, err)
		}
	} else {
		if err := downloadAndExtractAsset(downloadURL, assetName, "tar.gz", name, dest); err != nil {
			return BinarySetupResult{}, fmt.Errorf("install %s: %w", name, err)
		}
	}

	fmt.Fprintf(w, "  - %s: installed %s at %s\n", name, version, dest)
	return BinarySetupResult{Name: name, Path: dest, Version: version}, nil
}

func SetupNtfyBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "ntfy", "binwiederhier", "ntfy", []string{".tar.gz", ".zip"}, "tar.gz")
}

func SetupNebulaBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "nebula", "slackhq", "nebula", []string{".tar.gz", ".zip"}, "tar.gz")
}

func SetupRustFSBinary(w io.Writer) (BinarySetupResult, error) {
	name := "rustfs"
	if existing := findBinaryInPath(name); existing != "" {
		fmt.Fprintf(w, "  - %s: found in PATH at %s (skip install)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "path"}, nil
	}
	if existing, ok := findManagedBinary(name); ok {
		fmt.Fprintf(w, "  - %s: found in managed dir at %s (skip install)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "managed"}, nil
	}

	release, err := FetchLatestReleaseAllowPrereleases("rustfs", "rustfs")
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("fetch %s release metadata: %w", name, err)
	}
	version := strings.TrimPrefix(release.TagName, "v")

	dest, err := ManagedBinaryPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}

	if UseEgetForGitHubDownloads() {
		if err := tryEgetInstallGitHubTool(context.Background(), "rustfs", "rustfs", name, dest); err == nil {
			fmt.Fprintf(w, "  - %s: installed %s at %s (eget)\n", name, version, dest)
			return BinarySetupResult{Name: name, Path: dest, Version: version}, nil
		}
	}

	asset, err := findArchiveAssetForPlatform(release, []string{".zip"})
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("resolve %s asset for %s/%s: %w", name, runtime.GOOS, runtime.GOARCH, err)
	}
	if err := downloadAndExtractAsset(asset.DownloadURL, asset.Name, "zip", name, dest); err != nil {
		return BinarySetupResult{}, fmt.Errorf("install %s: %w", name, err)
	}

	fmt.Fprintf(w, "  - %s: installed %s at %s\n", name, version, dest)
	return BinarySetupResult{Name: name, Path: dest, Version: version}, nil
}

func SetupMinioBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "mc", "minio", "mc", []string{".tar.gz", ".zip"}, "tar.gz")
}

func SetupLokiBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "logcli", "grafana", "loki", []string{".zip"}, "zip")
}

func SetupPromtoolBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "promtool", "prometheus", "prometheus", []string{".tar.gz", ".zip"}, "tar.gz")
}

func SetupTraefikBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "traefik", "traefik", "traefik", []string{".tar.gz", ".zip"}, "tar.gz")
}

func setupNomadBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "nomad", "hashicorp", "nomad", []string{".zip"}, "zip")
}

func setupNodeProbeBinary(w io.Writer) (BinarySetupResult, error) {
	name := "abc-node-probe"
	if existing := findBinaryInPath(name); existing != "" {
		fmt.Fprintf(w, "  - %s: found in PATH at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "path"}, nil
	}

	cachedPath, err := DownloadReleaseAsset("abc-cluster", "abc-node-probe", "abc-node-probe")
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("download %s release asset: %w", name, err)
	}
	release, err := FetchLatestRelease("abc-cluster", "abc-node-probe")
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("resolve %s release version: %w", name, err)
	}

	dest, err := ManagedBinaryPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}
	if err := copyExecutable(cachedPath, dest); err != nil {
		return BinarySetupResult{}, err
	}
	version := strings.TrimPrefix(release.TagName, "v")
	fmt.Fprintf(w, "  - %s: installed %s at %s\n", name, version, dest)
	return BinarySetupResult{Name: name, Path: dest, Version: version}, nil
}

func setupGitHubArchiveBinary(w io.Writer, name, owner, repo string, assetSuffixes []string, extractMode string) (BinarySetupResult, error) {
	if existing := findBinaryInPath(name); existing != "" {
		fmt.Fprintf(w, "  - %s: found in PATH at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "path"}, nil
	}

	release, err := FetchLatestRelease(owner, repo)
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("fetch %s release metadata: %w", name, err)
	}
	version := strings.TrimPrefix(release.TagName, "v")

	dest, err := ManagedBinaryPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}

	if UseEgetForGitHubDownloads() {
		if err := tryEgetInstallGitHubTool(context.Background(), owner, repo, name, dest); err == nil {
			fmt.Fprintf(w, "  - %s: installed %s at %s (eget)\n", name, version, dest)
			return BinarySetupResult{Name: name, Path: dest, Version: version}, nil
		}
	}

	asset, err := findArchiveAssetForPlatform(release, assetSuffixes)
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("resolve %s asset for %s/%s: %w", name, runtime.GOOS, runtime.GOARCH, err)
	}
	if err := downloadAndExtractAsset(asset.DownloadURL, asset.Name, extractMode, name, dest); err != nil {
		return BinarySetupResult{}, fmt.Errorf("install %s: %w", name, err)
	}
	fmt.Fprintf(w, "  - %s: installed %s at %s\n", name, version, dest)
	return BinarySetupResult{Name: name, Path: dest, Version: version}, nil
}

func setupHashiUpBinary(w io.Writer) (BinarySetupResult, error) {
	name := "hashi-up"
	if existing := findBinaryInPath(name); existing != "" {
		fmt.Fprintf(w, "  - %s: found in PATH at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "path"}, nil
	}

	if existing, ok := findManagedBinary(name); ok {
		fmt.Fprintf(w, "  - %s: found in managed dir at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "managed"}, nil
	}

	if existing, ok := findManagedBinary(name); ok {
		fmt.Fprintf(w, "  - %s: found in managed dir at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "managed"}, nil
	}

	release, err := FetchLatestRelease("jsiebens", "hashi-up")
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("fetch %s release metadata: %w", name, err)
	}
	version := strings.TrimPrefix(release.TagName, "v")

	asset, err := findHashiUpAssetForPlatform(release)
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("resolve %s asset for %s/%s: %w", name, runtime.GOOS, runtime.GOARCH, err)
	}

	dest, err := ManagedBinaryPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}

	if err := downloadAndCopyAsset(asset.DownloadURL, asset.Name, dest); err != nil {
		return BinarySetupResult{}, fmt.Errorf("install %s: %w", name, err)
	}
	fmt.Fprintf(w, "  - %s: installed %s at %s\n", name, version, dest)
	return BinarySetupResult{Name: name, Path: dest, Version: version}, nil
}

func setupHashicorpBinaryViaHashiUp(w io.Writer, name, product string) (BinarySetupResult, error) {
	if existing := findBinaryInPath(name); existing != "" {
		fmt.Fprintf(w, "  - %s: found in PATH at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "path"}, nil
	}

	if existing, ok := findManagedBinary(name); ok {
		fmt.Fprintf(w, "  - %s: found in managed dir at %s (skip download)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "managed"}, nil
	}

	hashiUpBinary := resolveHashiUpBinary()
	if hashiUpBinary == "" {
		if _, err := setupHashiUpBinary(w); err != nil {
			return BinarySetupResult{}, fmt.Errorf("install hashi-up for %s: %w", name, err)
		}
		hashiUpBinary = resolveHashiUpBinary()
		if hashiUpBinary == "" {
			return BinarySetupResult{}, fmt.Errorf("hashi-up binary not found after install")
		}
	}

	tmpDir, err := os.MkdirTemp("", "abc-hashiup-*")
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	args := []string{product, "get", "--dest", tmpDir}
	if err := RunExternalCLI(context.Background(), args, hashiUpBinary, []string{"hashi-up", "hashiup"}, nil, w, w); err != nil {
		return BinarySetupResult{}, fmt.Errorf("hashi-up get %s: %w", product, err)
	}

	picked, err := findExtractedBinary(tmpDir, name)
	if err != nil {
		return BinarySetupResult{}, err
	}

	dest, err := ManagedBinaryPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}
	if err := copyExecutable(picked, dest); err != nil {
		return BinarySetupResult{}, err
	}

	fmt.Fprintf(w, "  - %s: installed via hashi-up at %s\n", name, dest)
	return BinarySetupResult{Name: name, Path: dest}, nil
}

func resolveHashiUpBinary() string {
	if p := findBinaryInPath("hashi-up"); p != "" {
		return p
	}
	if p := findBinaryInPath("hashiup"); p != "" {
		return p
	}
	if p, ok := findManagedBinary("hashi-up"); ok {
		return p
	}
	return ""
}

func findManagedBinary(name string) (string, bool) {
	p, err := ManagedBinaryPath(name)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return "", false
	}
	return p, true
}

func setupCargoInstalledBinary(w io.Writer, name, repoURL, bin string) (BinarySetupResult, error) {
	if existing := findBinaryInPath(name); existing != "" {
		fmt.Fprintf(w, "  - %s: found in PATH at %s (skip install)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "path"}, nil
	}

	if existing, ok := findManagedBinary(name); ok {
		fmt.Fprintf(w, "  - %s: found in managed dir at %s (skip install)\n", name, existing)
		return BinarySetupResult{Name: name, Path: existing, Skipped: true, SkipCause: "managed"}, nil
	}

	cargoPath, err := exec.LookPath("cargo")
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("cargo not found: %w", err)
	}

	cmd := exec.Command(cargoPath, "install", "--git", repoURL, "--bin", bin, "--locked")
	cmd.Env = os.Environ()
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return BinarySetupResult{}, fmt.Errorf("cargo install %s: %w", repoURL, err)
	}

	binPath, err := resolveCargoBinPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}

	dest, err := ManagedBinaryPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}
	if err := copyExecutable(binPath, dest); err != nil {
		return BinarySetupResult{}, err
	}
	fmt.Fprintf(w, "  - %s: installed via cargo install at %s\n", name, dest)
	return BinarySetupResult{Name: name, Path: dest}, nil
}

func resolveCargoBinPath(name string) (string, error) {
	cargoHome := os.Getenv("CARGO_HOME")
	if cargoHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		cargoHome = filepath.Join(home, ".cargo")
	}
	binary := filepath.Join(cargoHome, "bin", name)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("cargo binary not found at %s: %w", binary, err)
	}
	return binary, nil
}

func findExtractedBinary(root, name string) (string, error) {
	want := name
	if runtime.GOOS == "windows" {
		want = name + ".exe"
	}

	var picked string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Base(path) == want {
			picked = path
			return fs.SkipAll
		}
		return nil
	})
	if picked == "" {
		return "", fmt.Errorf("binary %q not found under %s", want, root)
	}
	return picked, nil
}

func findHashiUpAssetForPlatform(release *GitHubRelease) (*GitHubReleaseAsset, error) {
	name := "hashi-up"
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			name = "hashi-up-arm64"
		} else {
			name = "hashi-up-darwin"
		}
	case "linux":
		switch runtime.GOARCH {
		case "arm64":
			name = "hashi-up-arm64"
		case "arm":
			name = "hashi-up-armhf"
		default:
			name = "hashi-up"
		}
	case "windows":
		name = "hashi-up.exe"
	}
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return &release.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("matching asset not found")
}

func downloadAndCopyAsset(downloadURL, assetName, dest string) error {
	req, err := newGETRequest(downloadURL)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	resp, err := doRequestWithRetry(req)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download asset: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "abc-binary-*"+filepath.Ext(assetName))
	if err != nil {
		return fmt.Errorf("create temp asset: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp asset: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp asset: %w", err)
	}

	return copyExecutable(tmpPath, dest)
}

func findArchiveAssetForPlatform(release *GitHubRelease, archiveExts []string) (*GitHubReleaseAsset, error) {
	goos := normalizeGOOS(runtime.GOOS)
	goarch := normalizeGOARCH(runtime.GOARCH)

	archAliases := []string{goarch}
	switch goarch {
	case "amd64":
		archAliases = append(archAliases, "x86_64")
	case "arm64":
		archAliases = append(archAliases, "aarch64")
	}
	if goos == "darwin" {
		archAliases = append(archAliases, "all")
	}
	osAliases := []string{goos}
	if goos == "darwin" {
		// GitHub assets often use "osx" (e.g. rclone) or "macos" instead of "darwin".
		osAliases = append(osAliases, "macos", "osx")
	}

	extReParts := make([]string, 0, len(archiveExts))
	for _, ext := range archiveExts {
		extReParts = append(extReParts, regexp.QuoteMeta(strings.ToLower(ext)))
	}
	ext := strings.Join(extReParts, "|")
	for i := range release.Assets {
		n := strings.ToLower(release.Assets[i].Name)
		matchesExt := false
		for _, candidateExt := range archiveExts {
			if strings.HasSuffix(n, strings.ToLower(candidateExt)) {
				matchesExt = true
				break
			}
		}
		if !matchesExt {
			continue
		}
		for _, osAlias := range osAliases {
			for _, archAlias := range archAliases {
				re := regexp.MustCompile(fmt.Sprintf(`(^|[._-])%s([._-])%s([_.-]|$).*(%s)$`, regexp.QuoteMeta(osAlias), regexp.QuoteMeta(archAlias), ext))
				if re.MatchString(n) {
					return &release.Assets[i], nil
				}
			}
		}
	}

	for i := range release.Assets {
		n := strings.ToLower(release.Assets[i].Name)
		matchesExt := false
		for _, candidateExt := range archiveExts {
			if strings.HasSuffix(n, strings.ToLower(candidateExt)) {
				matchesExt = true
				break
			}
		}
		if !matchesExt {
			continue
		}
		for _, osAlias := range osAliases {
			if strings.Contains(n, osAlias) {
				return &release.Assets[i], nil
			}
		}
	}

	return nil, fmt.Errorf("matching asset not found")
}

// DownloadExtractBinary downloads an archive from downloadURL, extracts the
// binary named binaryName from it, and writes it as an executable to dest.
// Supported formats: .tar.gz / .tgz, .tar.bz2, .zip.
func DownloadExtractBinary(_ context.Context, downloadURL, assetName, binaryName, dest string) error {
	name := strings.ToLower(assetName)
	var mode string
	switch {
	case strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz"):
		mode = "tar.gz"
	case strings.HasSuffix(name, ".tar.bz2"):
		mode = "tar.bz2"
	case strings.HasSuffix(name, ".zip"):
		mode = "zip"
	default:
		return fmt.Errorf("unsupported archive format %q (want .tar.gz, .tar.bz2, or .zip)", assetName)
	}
	return downloadAndExtractAsset(downloadURL, assetName, mode, binaryName, dest)
}

func downloadAndExtractAsset(downloadURL, assetName, extractMode, binaryName, dest string) error {
	req, err := newGETRequest(downloadURL)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	resp, err := doRequestWithRetry(req)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download archive: HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "abc-binary-*"+filepath.Ext(assetName))
	if err != nil {
		return fmt.Errorf("create temp archive: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp archive: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp archive: %w", err)
	}

	switch extractMode {
	case "zip":
		return extractBinaryFromZip(tmpPath, binaryName, dest)
	case "tar.gz":
		return extractBinaryFromTarGz(tmpPath, binaryName, dest)
	case "tar.bz2":
		return extractBinaryFromTarBz2(tmpPath, binaryName, dest)
	default:
		return fmt.Errorf("unsupported extract mode %q", extractMode)
	}
}

func extractBinaryFromZip(archivePath, binaryName, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if filepath.Base(f.Name) != binaryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}
		defer rc.Close()
		return writeExecutable(rc, dest)
	}
	return fmt.Errorf("binary %q not found in zip", binaryName)
}

func extractBinaryFromTarGz(archivePath, binaryName, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar.gz: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		return writeExecutable(tr, dest)
	}
	return fmt.Errorf("binary %q not found in tar.gz", binaryName)
}

// DownloadAndExtractWithEget downloads an archive from downloadURL using the
// managed eget binary (~/.abc/binaries/eget), extracts binaryName from it, and
// writes the result as an executable to dest.
//
// Eget handles all archive formats (tar.gz, tar.bz2, zip, bare binaries)
// transparently, so no format-specific extraction code is needed here.
// The GitHub API is used upstream to select the right asset URL; eget receives
// that direct URL so it never has to guess between multiple release assets.
func DownloadAndExtractWithEget(ctx context.Context, downloadURL, binaryName, dest string) error {
	egetBin, err := ManagedBinaryPath("eget")
	if err != nil {
		return err
	}
	if _, err := os.Stat(egetBin); err != nil {
		return fmt.Errorf("eget not found at %s — run: abc admin tools fetch first", egetBin)
	}

	tmpDir, err := os.MkdirTemp("", "abc-eget-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Pass the direct download URL; eget extracts into tmpDir.
	// --file <binaryName> tells eget which binary to pull from the archive.
	args := []string{downloadURL, "--to", tmpDir + "/", "--file", binaryName}
	if err := RunExternalCLI(ctx, args, egetBin, []string{"eget"}, nil, io.Discard, io.Discard); err != nil {
		return fmt.Errorf("eget extract %q: %w", downloadURL, err)
	}

	extracted, err := findExtractedBinary(tmpDir, binaryName)
	if err != nil {
		return err
	}
	return copyExecutable(extracted, dest)
}

func extractBinaryFromTarBz2(archivePath, binaryName, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar.bz2: %w", err)
	}
	defer f.Close()

	tr := tar.NewReader(bzip2.NewReader(f))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		return writeExecutable(tr, dest)
	}
	return fmt.Errorf("binary %q not found in tar.bz2", binaryName)
}

func writeExecutable(src io.Reader, dest string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp binary: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("move binary into place: %w", err)
	}
	return nil
}

func copyExecutable(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source binary %q: %w", src, err)
	}
	defer f.Close()
	return writeExecutable(f, dest)
}

func findBinaryInPath(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}
