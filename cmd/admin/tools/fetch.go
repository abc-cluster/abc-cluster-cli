package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// archAliases maps normalised GOARCH values to the additional naming
// conventions used by GitHub release asset filenames across projects.
// e.g. s5cmd uses "64bit" for amd64, "32bit" for 386.
var archAliases = map[string][]string{
	// "-64" handles conda-forge/micromamba convention (linux-64, osx-64).
	// The leading dash prevents it matching inside "aarch64".
	"amd64": {"amd64", "x86_64", "x64", "64bit", "-64"},
	"386":   {"386", "i386", "i686", "32bit"},
	"arm64": {"arm64", "aarch64"},
	"arm":   {"arm", "armv6", "armhf", "armv7"},
}

// osAliases maps normalised GOOS values to naming conventions in asset filenames.
var osAliases = map[string][]string{
	"linux":   {"linux"},
	"darwin":  {"darwin", "macos", "osx", "macosx"},
	"windows": {"windows", "win"},
}

func newFetchCmd() *cobra.Command {
	var force bool
	var dryRun bool
	var archOverride []string

	cmd := &cobra.Command{
		Use:   "fetch [tool[@version]]",
		Short: "Download tools to local cache (~/.abc/binaries/)",
		Long: `Download tool binaries for all configured architectures using eget.

eget is bootstrapped automatically if not already present.

Examples:
  abc admin tools fetch                  # fetch all enabled tools
  abc admin tools fetch s5cmd            # fetch s5cmd only
  abc admin tools fetch s5cmd@v2.2.0    # specific version (overrides tools.toml)
  abc admin tools fetch --force          # re-download even if cached`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFetch(cmd.Context(), cmd.OutOrStdout(), args, force, dryRun, archOverride)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Re-download even if already cached")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be fetched without downloading")
	cmd.Flags().StringArrayVar(&archOverride, "arch", nil, "Override architectures (e.g. linux/amd64)")
	return cmd
}

func runFetch(ctx context.Context, w io.Writer, args []string, force, dryRun bool, archOverride []string) error {
	cfg, path, err := loadToolsConfig()
	if err != nil {
		return err
	}

	// Resolve architectures: flag > context config > default.
	arches := archOverride
	if len(arches) == 0 {
		activeCfg, cfgErr := config.Load()
		if cfgErr == nil {
			arches = activeCfg.ActiveCtx().ToolArchitectures()
		} else {
			arches = config.DefaultToolArchitectures()
		}
	}

	// Parse optional tool[@version] argument.
	filterName, filterVersion := "", ""
	if len(args) == 1 {
		parts := strings.SplitN(args[0], "@", 2)
		filterName = parts[0]
		if len(parts) == 2 {
			filterVersion = parts[1]
		}
	}

	tools := cfg.EnabledTools()
	if filterName != "" {
		spec, ok := cfg.ToolByName(filterName)
		if !ok {
			return fmt.Errorf("tool %q not found in tools.toml", filterName)
		}
		if filterVersion != "" {
			spec.Version = filterVersion
		}
		tools = []ToolSpec{spec}
	}

	if len(tools) == 0 {
		fmt.Fprintln(w, "No enabled tools in tools.toml.")
		return nil
	}

	// ── Step 1: ensure eget is bootstrapped (used by push for host-arch s5cmd) ─
	if _, err := ensureEget(ctx, w, cfg, dryRun); err != nil {
		return fmt.Errorf("bootstrap eget: %w", err)
	}

	// ── Step 2: resolve "latest" versions via GitHub API ─────────────────────
	fmt.Fprintln(w)
	resolvedVersions := make(map[string]string, len(tools))
	for _, t := range tools {
		if t.Version == "latest" {
			if dryRun {
				fmt.Fprintf(w, "  [dry-run] %s latest → (would resolve via GitHub API)\n", t.Name)
				resolvedVersions[t.Name] = "latest"
				continue
			}
			owner, repo, err := splitRepo(t.Repo)
			if err != nil {
				return err
			}
			release, err := utils.FetchLatestRelease(owner, repo)
			if err != nil {
				return fmt.Errorf("resolve latest %s: %w", t.Name, err)
			}
			resolvedVersions[t.Name] = release.TagName
			fmt.Fprintf(w, "  %s  latest → %s  (GitHub API)\n", t.Name, release.TagName)
		} else {
			resolvedVersions[t.Name] = t.Version
		}
	}

	// ── Step 3: download each tool × arch ────────────────────────────────────
	binDir, err := utils.AssetDir()
	if err != nil {
		return err
	}

	dirty := false // tracks whether tools.toml needs write-back

	fmt.Fprintln(w)
	for _, t := range tools {
		version := resolvedVersions[t.Name]
		fmt.Fprintf(w, "  %s  %s\n", t.Name, version)

		owner, repo, err := splitRepo(t.Repo)
		if err != nil {
			return err
		}

		for _, osArch := range arches {
			parts := strings.SplitN(osArch, "/", 2)
			if len(parts) != 2 {
				fmt.Fprintf(w, "    %s  ✗  invalid arch %q, skipping\n", osArch, osArch)
				continue
			}
			goos, goarch := parts[0], parts[1]
			binaryName := t.Name + "-" + goos + "-" + goarch
			destPath := filepath.Join(binDir, binaryName)

			if !force {
				if info, err := os.Stat(destPath); err == nil && !info.IsDir() && info.Size() > 0 {
					fmt.Fprintf(w, "    %-20s  ─  cached\n", osArch)
					continue
				}
			}

			if dryRun {
				fmt.Fprintf(w, "    %-20s  ○  would fetch via eget\n", osArch)
				continue
			}

			if err := fetchBinary(ctx, owner, repo, version, goos, goarch, t.Name, destPath); err != nil {
				fmt.Fprintf(w, "    %-20s  ✗  not available (%v)\n", osArch, err)
				continue
			}

			info, _ := os.Stat(destPath)
			size := ""
			if info != nil {
				size = fmt.Sprintf("~%s", humanBytes(info.Size()))
			}
			fmt.Fprintf(w, "    %-20s  ✓  → %s  (%s)\n", osArch, binaryName, size)
		}

		// Write back the resolved version if it changed from "latest".
		if !dryRun && t.Version == "latest" && version != "latest" {
			cfg.SetToolVersion(t.Name, version)
			dirty = true
		}
		fmt.Fprintln(w)
	}

	if dirty {
		if err := cfg.WriteBack(); err != nil {
			fmt.Fprintf(w, "[abc] warning: could not write back resolved versions to %s: %v\n", path, err)
		} else {
			fmt.Fprintf(w, "[abc] tools.toml updated with resolved versions (%s)\n", path)
		}
	}

	if !dryRun {
		fmt.Fprintf(w, "\nRun `abc admin tools push` to upload to the cluster.\n")
	}
	return nil
}

// ── eget bootstrap ────────────────────────────────────────────────────────────

// ensureEget returns the path to a working eget binary, bootstrapping it if needed.
// eget is stored as ~/.abc/binaries/eget (host arch only; not pushed to cluster).
func ensureEget(ctx context.Context, w io.Writer, cfg *ToolsConfig, dryRun bool) (string, error) {
	// Prefer eget on PATH (respects ABC_EGET_BINARY env as well).
	if p, err := exec.LookPath("eget"); err == nil {
		fmt.Fprintf(w, "[abc] eget found: %s\n", p)
		return p, nil
	}
	if v := strings.TrimSpace(os.Getenv("ABC_EGET_BINARY")); v != "" {
		if _, err := os.Stat(v); err == nil {
			fmt.Fprintf(w, "[abc] eget found (ABC_EGET_BINARY): %s\n", v)
			return v, nil
		}
	}

	// Check managed binary dir.
	egetPath, err := utils.ManagedBinaryPath("eget")
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(egetPath); err == nil && !info.IsDir() && info.Size() > 0 {
		fmt.Fprintf(w, "[abc] eget found: %s\n", egetPath)
		return egetPath, nil
	}

	if dryRun {
		fmt.Fprintf(w, "[abc] [dry-run] eget not found; would bootstrap %s\n", cfg.Engine.Version)
		return "(dry-run-eget)", nil
	}

	fmt.Fprintf(w, "[abc] bootstrapping eget %s...", cfg.Engine.Version)
	res, err := utils.SetupEgetBinary(w)
	if err != nil {
		return "", fmt.Errorf("bootstrap eget: %w", err)
	}
	fmt.Fprintf(w, "  done → %s\n", res.Path)
	return res.Path, nil
}

// ── eget invocation ───────────────────────────────────────────────────────────

// fetchBinary downloads a single binary for the given os/arch.
//
// Strategy:
//  1. Find the correct .tar.gz (or .zip on Windows) release asset via the
//     GitHub API, using extended arch aliases so unusual naming like "64bit"
//     for amd64 is handled correctly.
//  2. Download and extract the archive directly using the existing utils
//     infrastructure (no eget for cross-arch, avoids deb/rpm confusion).
//  3. eget is still used as the fetch engine for host-arch bootstrap (eget
//     itself, and the s5cmd push tool) via ensureEget / ensureS5cmd.
func fetchBinary(ctx context.Context, owner, repo, version, goos, goarch, binaryName, destPath string) error {
	// Resolve "latest" to a concrete tag.
	resolvedTag := version

	// Fetch release metadata.
	var release *utils.GitHubRelease
	var err error
	if resolvedTag == "latest" {
		release, err = utils.FetchLatestReleaseWithContext(ctx, owner, repo)
	} else {
		release, err = utils.FetchReleaseByTagWithContext(ctx, owner, repo, resolvedTag)
	}
	if err != nil {
		return fmt.Errorf("fetch release metadata: %w", err)
	}

	// Find the right archive asset for target os/arch.
	asset := findCrossArchAsset(release, goos, goarch)
	if asset == nil {
		return fmt.Errorf("no archive asset found for %s/%s in %s/%s@%s",
			goos, goarch, owner, repo, release.TagName)
	}

	// Download and extract using the managed eget binary.
	// Eget handles all archive formats (tar.gz, tar.bz2, zip, bare binaries)
	// without needing format-specific code here.
	return utils.DownloadAndExtractWithEget(ctx, asset.DownloadURL, binaryName, destPath)
}

// findCrossArchAsset searches release assets for an archive matching the target
// goos/goarch, using extended arch aliases. Skips package formats (.deb, .rpm …).
//
// When multiple archives match (e.g. musl vs glibc variants), the musl build is
// preferred — it is statically linked and more portable across cluster nodes.
func findCrossArchAsset(release *utils.GitHubRelease, goos, goarch string) *utils.GitHubReleaseAsset {
	osList := osAliases[strings.ToLower(goos)]
	if len(osList) == 0 {
		osList = []string{strings.ToLower(goos)}
	}
	archList := archAliases[strings.ToLower(goarch)]
	if len(archList) == 0 {
		archList = []string{strings.ToLower(goarch)}
	}

	archiveExts := []string{".tar.gz", ".tgz", ".zip", ".tar.bz2"}
	skipExts := []string{".deb", ".rpm", ".apk", ".msi", ".pkg"}

	// Collect all matching archives, then pick the musl build if present.
	var candidates []utils.GitHubReleaseAsset

	for _, asset := range release.Assets {
		n := strings.ToLower(asset.Name)

		// Skip checksums, signatures, and package formats.
		skip := false
		for _, sx := range skipExts {
			if strings.HasSuffix(n, sx) {
				skip = true
				break
			}
		}
		if skip || strings.HasSuffix(n, ".txt") || strings.HasSuffix(n, ".sig") ||
			strings.HasSuffix(n, ".sha256") || strings.HasSuffix(n, ".sha256sum") ||
			strings.HasSuffix(n, ".minisig") || strings.HasSuffix(n, ".asc") {
			continue
		}

		// Must be an archive format.
		isArchive := false
		for _, ax := range archiveExts {
			if strings.HasSuffix(n, ax) {
				isArchive = true
				break
			}
		}
		if !isArchive {
			continue
		}

		// Must contain an OS token and an arch token.
		hasOS := false
		for _, o := range osList {
			if strings.Contains(n, o) {
				hasOS = true
				break
			}
		}
		if !hasOS {
			continue
		}

		for _, a := range archList {
			if strings.Contains(n, a) {
				candidates = append(candidates, asset)
				break
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}
	// Prefer musl (statically linked, most portable on cluster nodes).
	for i := range candidates {
		if strings.Contains(strings.ToLower(candidates[i].Name), "musl") {
			return &candidates[i]
		}
	}
	return &candidates[0]
}

// egetChildEnv returns the environment for eget subprocesses, injecting
// GitHub token if configured.
func egetChildEnv() []string {
	env := os.Environ()
	// Reuse the token logic from the utils package via env vars that eget reads.
	// GITHUB_TOKEN and EGET_GITHUB_TOKEN are both honoured by eget.
	return env
}

// copyExecutable copies src to dst and marks it executable.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// splitRepo splits "owner/repo" into (owner, repo).
func splitRepo(fullRepo string) (string, string, error) {
	parts := strings.SplitN(fullRepo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo %q: expected owner/repo", fullRepo)
	}
	return parts[0], parts[1], nil
}

// hostBinaryName returns the canonical name for a binary on the current host.
func hostBinaryName(tool string) string {
	return tool + "-" + runtime.GOOS + "-" + runtime.GOARCH
}

// humanBytes formats a byte count as a human-readable string.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
