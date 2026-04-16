package utils

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	defaultManagedBinaryDir = ".abc/binaries"
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

func SetupNomadAndProbeBinaries(w io.Writer) ([]BinarySetupResult, error) {
	results := make([]BinarySetupResult, 0, 2)

	nomadRes, err := setupNomadBinary(w)
	if err != nil {
		return results, err
	}
	results = append(results, nomadRes)

	probeRes, err := setupNodeProbeBinary(w)
	if err != nil {
		return results, err
	}
	results = append(results, probeRes)
	return results, nil
}

func SetupTailscaleBinary(w io.Writer) (BinarySetupResult, error) {
	return setupGitHubArchiveBinary(w, "tailscale", "tailscale", "tailscale", []string{".tgz", ".tar.gz"}, "tar.gz")
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

	asset, err := findArchiveAssetForPlatform(release, assetSuffixes)
	if err != nil {
		return BinarySetupResult{}, fmt.Errorf("resolve %s asset for %s/%s: %w", name, runtime.GOOS, runtime.GOARCH, err)
	}

	dest, err := ManagedBinaryPath(name)
	if err != nil {
		return BinarySetupResult{}, err
	}
	if err := downloadAndExtractAsset(asset.DownloadURL, asset.Name, extractMode, name, dest); err != nil {
		return BinarySetupResult{}, fmt.Errorf("install %s: %w", name, err)
	}
	fmt.Fprintf(w, "  - %s: installed %s at %s\n", name, version, dest)
	return BinarySetupResult{Name: name, Path: dest, Version: version}, nil
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
	osAliases := []string{goos}
	if goos == "darwin" {
		osAliases = append(osAliases, "macos")
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
				re := regexp.MustCompile(fmt.Sprintf(`(^|[_-])%s([_-])%s([_.-]|$).*(%s)$`, regexp.QuoteMeta(osAlias), regexp.QuoteMeta(archAlias), ext))
				if re.MatchString(n) {
					return &release.Assets[i], nil
				}
			}
		}
	}
	return nil, fmt.Errorf("matching asset not found")
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
