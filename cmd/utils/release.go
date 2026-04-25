package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	// RequestTimeout is the timeout for GitHub API and asset download requests.
	RequestTimeout = 30 * time.Second
	// MaxRequestAttempts is the maximum number of HTTP attempts per request.
	MaxRequestAttempts = 3
)

var defaultHTTPClient = &http.Client{
	Timeout: RequestTimeout,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	},
}

// GitHubRelease represents a GitHub release from the API.
type GitHubRelease struct {
	TagName    string               `json:"tag_name"`
	Assets     []GitHubReleaseAsset `json:"assets"`
	Prerelease bool                 `json:"prerelease"`
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
	return FetchLatestReleaseWithContext(context.Background(), owner, repo)
}

// FetchLatestReleaseAllowPrereleases fetches the latest release information from GitHub,
// allowing prereleases if a stable release is not available.
func FetchLatestReleaseAllowPrereleases(owner, repo string) (*GitHubRelease, error) {
	return FetchLatestReleaseAllowPrereleasesWithContext(context.Background(), owner, repo)
}

// FetchLatestReleaseWithContext fetches the latest release information from GitHub
// using the provided context.
func FetchLatestReleaseWithContext(ctx context.Context, owner, repo string) (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", GitHubAPIURL, owner, repo)

	req, err := newGETRequest(url)
	if err != nil {
		return nil, fmt.Errorf("building release request: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := doRequestWithRetry(req)
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

// FetchLatestReleaseAllowPrereleasesWithContext fetches the newest GitHub release
// including prereleases by querying the release list when the /latest endpoint
// does not return a stable release.
func FetchLatestReleaseAllowPrereleasesWithContext(ctx context.Context, owner, repo string) (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=1", GitHubAPIURL, owner, repo)

	req, err := newGETRequest(url)
	if err != nil {
		return nil, fmt.Errorf("building release list request: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: %d - %s", resp.StatusCode, string(body))
	}

	var releases []GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decoding releases: %w", err)
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("no releases found for %s/%s", owner, repo)
	}

	return &releases[0], nil
}

// getPlatformBinaryName returns the expected binary name for the current platform.
// E.g., "abc-node-probe-linux-amd64" for Linux amd64.
func getPlatformBinaryName(binaryBase string) string {
	return getPlatformBinaryNameFor(binaryBase, runtime.GOOS, runtime.GOARCH)
}

func getPlatformBinaryNameFor(binaryBase, goos, goarch string) string {
	normOS := normalizeGOOS(goos)
	normArch := normalizeGOARCH(goarch)
	name := fmt.Sprintf("%s-%s-%s", binaryBase, normOS, normArch)
	if normOS == "windows" {
		name += ".exe"
	}
	return name
}

// findAssetForPlatform finds the release asset matching the current platform.
func findAssetForPlatform(release *GitHubRelease, binaryBase string) *GitHubReleaseAsset {
	return findAssetForPlatformTarget(release, binaryBase, runtime.GOOS, runtime.GOARCH)
}

func findAssetForPlatformTarget(release *GitHubRelease, binaryBase, goos, goarch string) *GitHubReleaseAsset {
	expectedName := getPlatformBinaryNameFor(binaryBase, goos, goarch)
	for i := range release.Assets {
		if release.Assets[i].Name == expectedName {
			return &release.Assets[i]
		}
	}
	// Fallback: exact name not found (older releases, renames) — match by prefix or tokens.
	normOS := normalizeGOOS(goos)
	normArch := normalizeGOARCH(goarch)
	prefix := strings.ToLower(fmt.Sprintf("%s-%s-%s", binaryBase, normOS, normArch))
	for i := range release.Assets {
		name := strings.ToLower(release.Assets[i].Name)
		if releaseAssetLikelyAuxiliary(name) {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			return &release.Assets[i]
		}
	}
	baseLower := strings.ToLower(binaryBase)
	osTok := strings.ToLower(normOS)
	archTok := strings.ToLower(normArch)
	for i := range release.Assets {
		name := strings.ToLower(release.Assets[i].Name)
		if releaseAssetLikelyAuxiliary(name) {
			continue
		}
		if strings.Contains(name, baseLower) && strings.Contains(name, osTok) && strings.Contains(name, archTok) {
			return &release.Assets[i]
		}
	}
	return nil
}

func releaseAssetLikelyAuxiliary(name string) bool {
	return strings.HasSuffix(name, ".txt") ||
		strings.HasSuffix(name, ".md") ||
		strings.HasSuffix(name, ".sig") ||
		strings.Contains(name, "sha256") ||
		strings.Contains(name, "checksum")
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

	preferName := getPlatformBinaryNameFor(binaryBase, runtime.GOOS, runtime.GOARCH)
	tryPath := filepath.Join(cacheDir, release.TagName, preferName)

	// Check if already cached and valid (preferred flat name first)
	if isCacheValid(tryPath, DefaultCacheTTL) {
		return tryPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(tryPath), 0755); err != nil {
		return "", fmt.Errorf("creating versioned cache directory: %w", err)
	}

	// Prefer eget (https://github.com/zyedidia/eget) when installed — better
	// asset matching and checksum verification for typical flat GitHub binaries.
	if UseEgetForGitHubDownloads() {
		if err := tryEgetDownloadFlatRelease(context.Background(), owner, repo, runtime.GOOS, runtime.GOARCH, preferName, tryPath); err == nil {
			return tryPath, nil
		}
	}

	asset := findAssetForPlatform(release, binaryBase)
	if asset == nil {
		return "", fmt.Errorf("no release asset found for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Cache path: ~/.cache/abc-cli/releases/<tag>/<binary-name>
	cachePath := filepath.Join(cacheDir, release.TagName, asset.Name)

	// Check if already cached and valid
	if cachePath != tryPath && isCacheValid(cachePath, DefaultCacheTTL) {
		return cachePath, nil
	}

	// Ensure the versioned cache directory exists
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return "", fmt.Errorf("creating versioned cache directory: %w", err)
	}

	// Download the asset
	req, err := newGETRequest(asset.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("building download request: %w", err)
	}

	resp, err := doRequestWithRetry(req)
	if err != nil {
		return "", fmt.Errorf("downloading asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Write to temporary file first, then move to final location
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), asset.Name+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("creating cache file: %w", err)
	}
	tmpFile := tmp.Name()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpFile)
		return "", fmt.Errorf("writing cache file: %w", err)
	}

	// Validate size when GitHub reports it to catch truncated downloads.
	if asset.Size > 0 {
		info, err := tmp.Stat()
		if err != nil {
			tmp.Close()
			os.Remove(tmpFile)
			return "", fmt.Errorf("reading temp file size: %w", err)
		}
		if info.Size() != int64(asset.Size) {
			tmp.Close()
			os.Remove(tmpFile)
			return "", fmt.Errorf("downloaded size mismatch: expected %d bytes, got %d bytes", asset.Size, info.Size())
		}
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpFile)
		return "", fmt.Errorf("syncing cache file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("closing cache file: %w", err)
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

// GetLatestReleaseAssetURL returns the asset download URL and version for the latest release.
func GetLatestReleaseAssetURL(owner, repo, binaryBase string) (string, string, error) {
	return GetLatestReleaseAssetURLForPlatform(owner, repo, binaryBase, runtime.GOOS, runtime.GOARCH)
}

// GetLatestReleaseAssetURLForPlatform returns the asset URL and version for a target platform.
func GetLatestReleaseAssetURLForPlatform(owner, repo, binaryBase, goos, goarch string) (string, string, error) {
	release, err := FetchLatestRelease(owner, repo)
	if err != nil {
		return "", "", err
	}
	asset := findAssetForPlatformTarget(release, binaryBase, goos, goarch)
	if asset == nil {
		return "", "", fmt.Errorf("no release asset found for platform %s/%s", normalizeGOOS(goos), normalizeGOARCH(goarch))
	}
	version := strings.TrimPrefix(release.TagName, "v")
	return asset.DownloadURL, version, nil
}

func normalizeGOOS(goos string) string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "macos", "osx":
		return "darwin"
	default:
		return strings.ToLower(strings.TrimSpace(goos))
	}
}

func normalizeGOARCH(goarch string) string {
	switch strings.ToLower(strings.TrimSpace(goarch)) {
	case "x86_64", "x64":
		return "amd64"
	case "aarch64", "armv8":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(goarch))
	}
}

func newGETRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "abc-cluster-cli")

	if token, ok := getGitHubToken(); ok && req.URL.Host == "api.github.com" && req.URL.Scheme == "https" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", token))
	}

	return req, nil
}

func doRequestWithRetry(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= MaxRequestAttempts; attempt++ {
		tryReq := req.Clone(req.Context())
		resp, err := defaultHTTPClient.Do(tryReq)
		retryAfterSeconds := 0
		if err != nil {
			lastErr = err
		} else if !shouldRetryStatus(resp.StatusCode) {
			return resp, nil
		} else {
			lastErr = fmt.Errorf("request failed with status %d", resp.StatusCode)
			retryAfterSeconds = respRetryAfterSeconds(resp)
			resp.Body.Close()
		}

		if attempt < MaxRequestAttempts {
			time.Sleep(retryDelay(attempt, retryAfterSeconds))
		}
	}

	return nil, lastErr
}

func shouldRetryStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func retryDelay(attempt int, retryAfterSeconds int) time.Duration {
	if retryAfterSeconds > 0 {
		return time.Duration(retryAfterSeconds) * time.Second
	}

	// Simple bounded exponential backoff.
	delay := time.Duration(1<<uint(attempt-1)) * time.Second
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

func respRetryAfterSeconds(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	secs, convErr := strconv.Atoi(value)
	if convErr != nil || secs < 0 {
		return 0
	}
	return secs
}

func getGitHubToken() (string, bool) {
	raw := os.Getenv("EGET_GITHUB_TOKEN")
	if raw == "" {
		raw = os.Getenv("GITHUB_TOKEN")
	}
	if raw == "" {
		return "", false
	}

	token, err := tokenFrom(raw)
	if err != nil {
		return "", false
	}
	return token, token != ""
}

func tokenFrom(input string) (string, error) {
	if strings.HasPrefix(input, "@") {
		tokenPath := strings.TrimPrefix(input, "@")
		data, err := os.ReadFile(tokenPath)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(input), nil
}
