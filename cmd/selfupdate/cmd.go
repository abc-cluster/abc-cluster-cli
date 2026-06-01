// Package selfupdate implements `abc self-update` — in-place upgrade of the
// running abc binary from GitHub releases.
//
// Replaces the copy-paste curl install command that the update-available notice
// used to print. The flow:
//
//	1. Resolve the running version (state.CLIVersion) and the target release
//	   (latest, or a pinned --version tag).
//	2. Compare; skip if already current (unless --version forces a specific tag).
//	3. Find the release asset for the running OS/arch (abc-<os>-<arch>).
//	4. Download to a temp file in the same directory as the running binary
//	   (so the swap is an atomic same-filesystem rename).
//	5. Atomically replace the running binary. On EACCES (binary in a root-owned
//	   dir like /usr/local/bin), retry the final move via sudo when a TTY is
//	   present, else print the exact `sudo mv` to run.
//
// Replacing a running executable by rename is safe on Linux/macOS: the kernel
// keeps the open inode for the live process while the path points at the new
// file. Windows cannot replace a running .exe in place — there we download and
// print the manual move instruction instead.
package selfupdate

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

const (
	repoOwner  = "abc-cluster"
	repoName   = "abc-cluster-cli"
	binaryBase = "abc"
	fetchTimeout = 15 * time.Second
)

// NewCmd returns the `abc self-update` command.
func NewCmd() *cobra.Command {
	var (
		pinVersion string
		checkOnly  bool
		assumeYes  bool
	)

	cmd := &cobra.Command{
		Use:     "self-update",
		Aliases: []string{"selfupdate", "upgrade"},
		Short:   "Update the abc CLI to the latest release (in place)",
		Long: `Download the latest abc release and replace the running binary in place.

This is the convenient alternative to the curl install command printed by the
update-available notice — it figures out your platform, downloads the right
binary, and swaps it atomically.

Examples:
  abc self-update                 # upgrade to the latest release
  abc self-update --check         # report current vs latest; download nothing
  abc self-update --version v0.1.33   # install a specific tag (up- or downgrade)
  abc self-update --yes           # skip the confirmation prompt

If the binary lives in a root-owned directory (e.g. /usr/local/bin), the final
move is retried with sudo when run interactively, or printed for you to run.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSelfUpdate(cmd, pinVersion, checkOnly, assumeYes)
		},
	}

	cmd.Flags().StringVar(&pinVersion, "version", "", "install a specific release tag (e.g. v0.1.33) instead of latest")
	cmd.Flags().BoolVar(&checkOnly, "check", false, "report current vs latest version and exit without downloading")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func runSelfUpdate(cmd *cobra.Command, pinVersion string, checkOnly, assumeYes bool) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()

	currentRaw := state.CLIVersion
	currentTag := baseSemverTag(currentRaw)
	if currentTag == "" {
		fmt.Fprintf(errOut, "[abc] running a dev build (%q) — self-update targets released versions.\n", currentRaw)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), fetchTimeout)
	defer cancel()

	// Resolve the target release: pinned tag or latest.
	var (
		release *utils.GitHubRelease
		err     error
	)
	if pinVersion != "" {
		tag := utils.EnsureVPrefix(strings.TrimSpace(pinVersion))
		release, err = utils.FetchReleaseByTagWithContext(ctx, repoOwner, repoName, tag)
		if err != nil {
			return fmt.Errorf("fetching release %s: %w", tag, err)
		}
	} else {
		release, err = utils.FetchLatestReleaseWithContext(ctx, repoOwner, repoName)
		if err != nil {
			return fmt.Errorf("fetching latest release: %w", err)
		}
	}
	targetTag := utils.EnsureVPrefix(strings.TrimSpace(release.TagName))

	// Compare. With an explicit --version we always proceed (allows downgrade /
	// re-pin); without it we skip when the current release is already >= target.
	if pinVersion == "" && currentTag != "" && semver.IsValid(currentTag) && semver.IsValid(targetTag) {
		if semver.Compare(currentTag, targetTag) >= 0 {
			fmt.Fprintf(out, "[abc] already up to date (current %s, latest %s)\n",
				currentTag, targetTag)
			return nil
		}
	}

	fmt.Fprintf(out, "[abc] current: %s\n", displayVersion(currentRaw, currentTag))
	fmt.Fprintf(out, "[abc] target:  %s\n", targetTag)

	if checkOnly {
		fmt.Fprintf(out, "[abc] update available — run `abc self-update` to install.\n")
		return nil
	}

	// Locate the asset for this platform.
	assetURL, assetSize, err := utils.FindReleaseAssetURL(release, binaryBase, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	// Resolve the running binary's real path (follow symlinks so we replace the
	// actual file, not a symlink that points at it).
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating running binary: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exePath); rerr == nil {
		exePath = resolved
	}

	if runtime.GOOS == "windows" {
		return windowsManualInstall(out, assetURL, exePath, targetTag)
	}

	if !assumeYes {
		fmt.Fprintf(out, "[abc] replace %s with %s? [y/N] ", exePath, targetTag)
		if !confirm(cmd) {
			fmt.Fprintln(out, "[abc] aborted.")
			return nil
		}
	}

	destDir := filepath.Dir(exePath)
	// If the install directory isn't writable, download to a system temp dir and
	// finish with sudo; otherwise download alongside the binary for an atomic
	// same-filesystem rename.
	downloadDir := destDir
	needsPrivilege := !dirWritable(destDir)
	if needsPrivilege {
		downloadDir = os.TempDir()
	}

	fmt.Fprintf(out, "[abc] downloading %s (%s)…\n", filepath.Base(assetURL), humanSize(assetSize))
	tmpPath, err := utils.DownloadAssetToFile(ctx, assetURL, downloadDir, assetSize)
	if err != nil {
		return err
	}

	if needsPrivilege {
		if err := privilegedReplace(out, errOut, tmpPath, exePath); err != nil {
			return err
		}
	} else {
		if err := os.Rename(tmpPath, exePath); err != nil {
			os.Remove(tmpPath)
			// Race: dir looked writable but rename was denied. Fall back to sudo.
			if os.IsPermission(err) {
				tmpPath2, derr := utils.DownloadAssetToFile(ctx, assetURL, os.TempDir(), assetSize)
				if derr != nil {
					return derr
				}
				if perr := privilegedReplace(out, errOut, tmpPath2, exePath); perr != nil {
					return perr
				}
			} else {
				return fmt.Errorf("replacing binary: %w", err)
			}
		}
	}

	fmt.Fprintf(out, "[abc] updated to %s ✓\n", targetTag)
	// Confirm the new binary runs and reports the expected version.
	if v, verr := exec.Command(exePath, "--version").Output(); verr == nil {
		fmt.Fprintf(out, "[abc] %s", v)
	}
	return nil
}

// privilegedReplace moves tmpPath over dest using sudo. Requires an interactive
// TTY for the password prompt; otherwise prints the exact command to run.
func privilegedReplace(out, errOut io.Writer, tmpPath, dest string) error {
	if !isInteractive() {
		fmt.Fprintf(errOut, "[abc] %s is not writable by this user.\n", filepath.Dir(dest))
		fmt.Fprintf(errOut, "[abc] downloaded to %s — finish the update with:\n", tmpPath)
		fmt.Fprintf(errOut, "  sudo mv %s %s && sudo chmod 0755 %s\n", tmpPath, dest, dest)
		return fmt.Errorf("insufficient permissions to replace %s (no TTY for sudo)", dest)
	}
	fmt.Fprintf(out, "[abc] %s is root-owned — finishing with sudo (you may be prompted)…\n", filepath.Dir(dest))
	mv := exec.Command("sudo", "mv", tmpPath, dest)
	mv.Stdin, mv.Stdout, mv.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := mv.Run(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("sudo mv failed: %w", err)
	}
	ch := exec.Command("sudo", "chmod", "0755", dest)
	ch.Stdin, ch.Stdout, ch.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = ch.Run()
	return nil
}

func windowsManualInstall(out io.Writer, assetURL, exePath, tag string) error {
	fmt.Fprintf(out, "[abc] Windows cannot replace a running .exe in place.\n")
	fmt.Fprintf(out, "[abc] download %s and overwrite:\n  %s\n", tag, exePath)
	fmt.Fprintf(out, "  %s\n", assetURL)
	return nil
}

// baseSemverTag extracts a clean vMAJOR.MINOR.PATCH tag from a build version
// string like "v0.1.33-4-g572e7b7 (572e7b7)" or "v0.1.33". Returns "" for dev
// builds with no recognisable tag.
func baseSemverTag(raw string) string {
	v := strings.TrimSpace(raw)
	if i := strings.IndexAny(v, " ("); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if v == "" || strings.EqualFold(v, "dev") {
		return ""
	}
	v = utils.EnsureVPrefix(v)
	// Strip a git-describe suffix (-N-gSHA) so we compare on the release tag.
	if i := strings.Index(v, "-"); i >= 0 {
		base := v[:i]
		if semver.IsValid(base) {
			return semver.Canonical(base)
		}
	}
	if semver.IsValid(v) {
		return semver.Canonical(v)
	}
	return ""
}

func displayVersion(raw, tag string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return tag
	}
	return raw
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".abc-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func confirm(cmd *cobra.Command) bool {
	var resp string
	// Read a single token from stdin; empty / non-yes is "no".
	if _, err := fmt.Fscanln(cmd.InOrStdin(), &resp); err != nil {
		return false
	}
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes"
}

func humanSize(bytes int) string {
	if bytes <= 0 {
		return "size unknown"
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := int64(bytes) / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGT"[exp])
}
