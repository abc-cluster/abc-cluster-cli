package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// GitHubAPIURL is the base URL for GitHub API calls.
	GitHubAPIURL = "https://api.github.com"
	// GitHubRawURL is the base URL for raw GitHub content.
	GitHubRawURL = "https://raw.githubusercontent.com"
	// DefaultCacheTTL is the default time-to-live for cached binaries.
	DefaultCacheTTL = 24 * time.Hour
)

// GitHubRelease represents a GitHub release from the API.
type GitHubRelease struct {
	TagName string                `json:"tag_name"`
	Assets  []GitHubReleaseAsset   `json:"assets"`
	Prerelease bool                `json:"prerelease"`
}

// GitHubReleaseAsset represents a release asset (binary).
type GitHubReleaseAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int    `json:"size"`
}

// getReleaseCache returns the path where release binaries are cached.
// Creates the directory if it doesn't exist.
func getReleaseCache() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	cacheDir := filepath.Join(home, ".cache", "abc-cli", "releases")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("creating cache directory: %w", err)
	}
	return cacheDir, nil
}

// isCacheValid checks if a cached binary is still valid (not expired).
func isCacheValid(filePath string, ttl time.Duration) bool {
	info, err := os.Stat(filePath)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < ttl
}

// FetchLatestRelease fetches the latest release information from GitHub.
func FetchLatestRelease(owner, repo string) (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", GitHubAPIURL, owner, repo)
	
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching release info: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: %d - %s", resp.StatusCode, string(body))
	}
	
	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}
	
	return &release, nil
}

// getPlatformBinaryName returns the expected binary name for the current platform.
// E.g., "abc-node-probe-linux-amd64" for Linux amd64.
func getPlatformBinaryName(binaryBase string) string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	
	// Normalize architectures to match Makefile naming
	if goarch == "amd64" {
		goarch = "amd64"
	} else if goarch == "arm64" {
		goarch = "arm64"
	}
	
	name := fmt.Sprintf("%s-%s-%s", binaryBase, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// findAssetForPlatform finds the release asset matching the current platform.
func findAssetForPlatform(release *GitHubRelease, binaryBase string) *GitHubReleaseAsset {
	expectedName := getPlatformBinaryName(binaryBase)
	for i, asset := range release.Assets {
		if asset.Name == expectedName {
			return &release.Assets[i]
		}
	}
	return nil
}

// DownloadReleaseAsset downloads a release asset and caches it locally.
// Returns the path to the cached binary, or uses an already cached version if valid.
func DownloadReleaseAsset(owner, repo, binaryBase string) (string, error) {
	cacheDir, err := getReleaseCache()
	if err != nil {
		return "", err
	}
	
	release, err := FetchLatestRelease(owner, repo)
	if err != nil {
		return "", err
	}
	
	asset := findAssetForPlatform(release, binaryBase)
	if asset == nil {
		return "", fmt.Errorf("no release asset found for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	
	// Cache path: ~/.cache/abc-cli/releases/<tag>/<binary-name>
	cachePath := filepath.Join(cacheDir, release.TagName, asset.Name)
	
	// Check if already cached and valid
	if isCacheValid(cachePath, DefaultCacheTTL) {
		return cachePath, nil
	}
	
	// Ensure the versioned cache directory exists
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return "", fmt.Errorf("creating versioned cache directory: %w", err)
	}
	
	// Download the asset
	resp, err := http.Get(asset.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("downloading asset: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	
	// Write to temporary file first, then move to final location
	tmpFile := cachePath + ".tmp"
	file, err := os.Create(tmpFile)
	if err != nil {
		return "", fmt.Errorf("creating cache file: %w", err)
	}
	defer file.Close()
	
	if _, err := io.Copy(file, resp.Body); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("writing cache file: %w", err)
	}
	
	// Make executable
	if err := os.Chmod(tmpFile, 0755); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("making executable: %w", err)
	}
	
	// Move from temp to final location
	if err := os.Rename(tmpFile, cachePath); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("finalizing cached file: %w", err)
	}
	
	return cachePath, nil
}

// GetOrDownloadReleaseBinary gets the probe binary either from cache or by downloading the latest release.
// It attempts to download first, but falls back to preInstalledPath if download fails.
func GetOrDownloadReleaseBinary(owner, repo, binaryBase, preInstalledPath string) string {
	// Try to download the latest release
	binaryPath, err := DownloadReleaseAsset(owner, repo, binaryBase)
	if err == nil {
		return binaryPath
	}
	
	// Fall back to pre-installed path if available
	if _, err := os.Stat(preInstalledPath); err == nil {
		return preInstalledPath
	}
	
	// If neither works, return the pre-installed path anyway (let it fail at runtime)
	return preInstalledPath
}

// GetReleaseVersion fetches the version of the latest release.
func GetReleaseVersion(owner, repo string) (string, error) {
	release, err := FetchLatestRelease(owner, repo)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(release.TagName, "v"), nil
}
