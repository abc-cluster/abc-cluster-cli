package utils

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Optional integration with https://github.com/zyedidia/eget for GitHub release
// downloads: better asset detection and extraction (archives + flat binaries)
// when the eget binary is available.
//
// Environment:
//   ABC_USE_EGET     unset or "auto" — use eget when on PATH (or ABC_EGET_BINARY)
//                    "0"/"false"/"no"/"off" — never use eget
//   ABC_EGET_BINARY  path to eget executable (defaults to PATH lookup)
//
// GitHub auth for eget matches eget's expectations: EGET_GITHUB_TOKEN or
// GITHUB_TOKEN (abc already reads these in getGitHubToken).

func egetExecutable() string {
	if v := strings.TrimSpace(os.Getenv("ABC_EGET_BINARY")); v != "" {
		return v
	}
	p, err := exec.LookPath("eget")
	if err != nil {
		return ""
	}
	return p
}

// UseEgetForGitHubDownloads reports whether abc should prefer invoking eget for
// GitHub release installs when possible.
func UseEgetForGitHubDownloads() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ABC_USE_EGET"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return egetExecutable() != ""
	}
}

func egetChildEnv() []string {
	env := os.Environ()
	if t, ok := getGitHubToken(); ok {
		if strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) == "" {
			env = append(env, "GITHUB_TOKEN="+t)
		}
		if strings.TrimSpace(os.Getenv("EGET_GITHUB_TOKEN")) == "" {
			env = append(env, "EGET_GITHUB_TOKEN="+t)
		}
	}
	return env
}

// tryEgetDownloadFlatRelease runs eget against owner/repo for a single flat
// release asset (no archive), writing the result to destFile.
func tryEgetDownloadFlatRelease(ctx context.Context, owner, repo, goos, goarch, wantFileBase, destFile string) error {
	exe := egetExecutable()
	if exe == "" {
		return fmt.Errorf("eget not found")
	}
	tmpDir, err := os.MkdirTemp("", "abc-eget-flat-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	sys := normalizeGOOS(goos) + "/" + normalizeGOARCH(goarch)
	args := []string{owner + "/" + repo, "--system", sys, "--to", tmpDir, "-f", wantFileBase, "--quiet"}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = egetChildEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("eget %v: %w", args, err)
	}

	var picked string
	_ = filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Base(path) == wantFileBase {
			picked = path
			return fs.SkipAll
		}
		return nil
	})
	if picked == "" {
		return fmt.Errorf("eget did not produce %q under %s", wantFileBase, tmpDir)
	}
	return copyExecutable(picked, destFile)
}

// tryEgetInstallGitHubTool runs eget for owner/repo, extracts the tool named
// wantBinaryName (Nomad archive layout, tailscale tarball, etc.), and copies
// it to destFile.
func tryEgetInstallGitHubTool(ctx context.Context, owner, repo, wantBinaryName, destFile string) error {
	exe := egetExecutable()
	if exe == "" {
		return fmt.Errorf("eget not found")
	}
	tmpDir, err := os.MkdirTemp("", "abc-eget-tool-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	sys := normalizeGOOS(runtime.GOOS) + "/" + normalizeGOARCH(runtime.GOARCH)
	args := []string{owner + "/" + repo, "--system", sys, "--to", tmpDir, "--quiet"}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = egetChildEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("eget %v: %w", args, err)
	}

	var picked string
	_ = filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Base(path) == wantBinaryName {
			picked = path
			return fs.SkipAll
		}
		return nil
	})
	if picked == "" {
		return fmt.Errorf("eget did not produce executable %q under %s", wantBinaryName, tmpDir)
	}
	return copyExecutable(picked, destFile)
}
