package node

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// PreflightResult captures the state of the target before installation.
type PreflightResult struct {
	OS                 string // "linux", "darwin", "windows"
	Arch               string // "amd64", "arm64", etc.
	InitSystem         string // "systemd", "launchd", "none"
	HasSudo            bool
	CanInstallPkgs     bool   // user can run apt/yum/brew install (via sudo)
	PkgManager         string // detected package manager: "apt", "yum", "dnf", "brew", "none"
	NomadInstalled     bool
	TailscaleInstalled bool
	TailscaleConnected bool
}

// RunPreflight executes minimal checks on the target needed for Nomad install.
// Borrows the check pattern from abc-node-probe, stripped to the install minimum.
func RunPreflight(ctx context.Context, ex Executor, w io.Writer) (*PreflightResult, error) {
	res := &PreflightResult{
		OS:   ex.OS(),
		Arch: ex.Arch(),
	}

	fmt.Fprintf(w, "\n  Preflight:\n")
	fmt.Fprintf(w, "    ✓ OS              %s/%s\n", res.OS, res.Arch)

	// Init system
	switch res.OS {
	case "linux":
		var b strings.Builder
		_ = ex.Run(ctx, "cat /proc/1/comm 2>/dev/null", &b)
		comm := strings.TrimSpace(b.String())
		if strings.Contains(comm, "systemd") {
			res.InitSystem = "systemd"
			fmt.Fprintf(w, "    ✓ Init system     systemd\n")
		} else {
			res.InitSystem = comm
			fmt.Fprintf(w, "    ✗ Init system     %q (not systemd — service registration unavailable)\n", comm)
		}
	case "darwin":
		res.InitSystem = "launchd"
		fmt.Fprintf(w, "    ✓ Init system     launchd\n")
	default:
		res.InitSystem = "none"
		fmt.Fprintf(w, "    - Init system     manual (Windows — sc.exe instructions will be printed)\n")
	}

	// Sudo / admin access
	if res.OS == "windows" {
		var b strings.Builder
		_ = ex.Run(ctx, "whoami /groups 2>&1", &b)
		// S-1-16-12288 is the High Mandatory Level SID (Administrator)
		if strings.Contains(b.String(), "S-1-16-12288") {
			res.HasSudo = true
			fmt.Fprintf(w, "    ✓ Sudo access     ok (Administrator)\n")
		} else {
			fmt.Fprintf(w, "    ✗ Sudo access     not Administrator — re-run as Administrator\n")
		}
	} else {
		if err := ex.Run(ctx, "sudo -n true 2>/dev/null", io.Discard); err == nil {
			res.HasSudo = true
			fmt.Fprintf(w, "    ✓ Sudo access     ok\n")
		} else {
			fmt.Fprintf(w, "    ✗ Sudo access     sudo required\n")
		}
	}

	// Package manager + install permission check
	// We only need this when Nomad is not already installed (curl + unzip must be present,
	// and the user must be able to write to /usr/local/bin via sudo).
	// The check is purely informational on Windows — we install via direct binary upload.
	if res.OS != "windows" {
		res.PkgManager, res.CanInstallPkgs = checkPackageAccess(ctx, ex, w, res.HasSudo)
	} else {
		res.PkgManager = "none"
		res.CanInstallPkgs = res.HasSudo
	}

	// Nomad already installed?
	{
		var b strings.Builder
		err := ex.Run(ctx, "command -v nomad 2>/dev/null", &b)
		if err == nil && strings.TrimSpace(b.String()) != "" {
			res.NomadInstalled = true
			fmt.Fprintf(w, "    ✓ Nomad           already installed (will skip Nomad install)\n")
		} else {
			fmt.Fprintf(w, "    - Nomad           not installed\n")
		}
	}

	// Tailscale already installed?
	{
		var b strings.Builder
		err := ex.Run(ctx, "command -v tailscale 2>/dev/null", &b)
		if err == nil && strings.TrimSpace(b.String()) != "" {
			res.TailscaleInstalled = true
			var sb strings.Builder
			_ = ex.Run(ctx, "tailscale status 2>/dev/null | head -5", &sb)
			if strings.Contains(sb.String(), "100.") {
				res.TailscaleConnected = true
				fmt.Fprintf(w, "    ✓ Tailscale       installed and connected (skip)\n")
			} else {
				fmt.Fprintf(w, "    ✓ Tailscale       installed (will run tailscale up)\n")
			}
		} else {
			fmt.Fprintf(w, "    - Tailscale       not installed\n")
		}
	}

	// Hard stops
	if !res.HasSudo && res.OS != "windows" {
		return res, fmt.Errorf(`sudo access required on %s — aborting

  The SSH user lacks passwordless sudo. To fix this, either:
    1. Add the user to the sudoers file on the remote host:
         echo "<user> ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/<user>
    2. Connect as root: abc node add --host=%s --user=root
    3. Use --skip-preflight if you have already configured sudo`, res.OS, res.OS)
	}
	if res.OS == "linux" && res.InitSystem != "systemd" {
		return res, fmt.Errorf(`systemd required on Linux for automatic service registration (found init: %q)

  Options:
    • Use --skip-enable --skip-start to install the binary and config only
    • Start Nomad manually: sudo /usr/local/bin/nomad agent -config /etc/nomad.d
    • Use --skip-preflight to bypass this check if you know what you are doing`, res.InitSystem)
	}
	if !res.CanInstallPkgs && !res.NomadInstalled && res.OS != "windows" {
		// Non-fatal warning — Nomad is installed via direct binary upload (curl + unzip),
		// not via apt/yum. But warn clearly so the user understands the state.
		fmt.Fprintf(w, "\n  Warning: Could not verify package-install privileges.\n")
		fmt.Fprintf(w, "  Nomad binary will be uploaded directly — no package manager needed.\n")
		fmt.Fprintf(w, "  If the install fails with permission errors, check sudo access.\n")
	}

	return res, nil
}

// checkPackageAccess detects the available package manager and verifies the user
// can run privileged install commands. Returns (pkgManager, canInstall).
//
// We don't actually install via the package manager — Nomad is downloaded as a
// binary. But Tailscale's install.sh uses curl | sh which needs apt/yum to pull
// in tailscaled's dependencies. This check catches permission issues early.
func checkPackageAccess(ctx context.Context, ex Executor, w io.Writer, hasSudo bool) (pkgMgr string, canInstall bool) {
	// Detect package manager
	pkgMgr = detectPkgManager(ctx, ex)

	if pkgMgr == "none" {
		fmt.Fprintf(w, "    ? Pkg manager     none detected (Nomad installed via direct binary upload)\n")
		return pkgMgr, hasSudo // binary upload only requires sudo for write to /usr/local/bin
	}

	if !hasSudo {
		fmt.Fprintf(w, "    ✗ Pkg manager     %s detected but user lacks sudo — Tailscale install may fail\n", pkgMgr)
		fmt.Fprintf(w, "                      Tip: ensure the SSH user has NOPASSWD sudo, or omit --tailscale\n")
		return pkgMgr, false
	}

	// Verify the user can actually run the package manager with sudo (dry-run / list)
	var testCmd string
	switch pkgMgr {
	case "apt":
		testCmd = "sudo apt-get -qq -s install curl 2>&1 | head -3"
	case "dnf":
		testCmd = "sudo dnf -q check-update curl 2>&1 | head -3 || true"
	case "yum":
		testCmd = "sudo yum -q check-update curl 2>&1 | head -3 || true"
	case "brew":
		testCmd = "brew list --versions curl 2>/dev/null | head -1"
	}

	var out strings.Builder
	err := ex.Run(ctx, testCmd, &out)
	if err != nil {
		output := strings.TrimSpace(out.String())
		fmt.Fprintf(w, "    ✗ Pkg manager     %s found but privilege check failed\n", pkgMgr)
		if output != "" {
			fmt.Fprintf(w, "                      %s\n", firstLine(output))
		}
		fmt.Fprintf(w, "                      Tip: verify the SSH user can run 'sudo %s install <pkg>'\n", pkgMgr)
		fmt.Fprintf(w, "                      Or add to sudoers: echo \"<user> ALL=(ALL) NOPASSWD: /usr/bin/%s\" | sudo tee /etc/sudoers.d/<user>-%s\n", pkgMgr, pkgMgr)
		return pkgMgr, false
	}

	fmt.Fprintf(w, "    ✓ Pkg manager     %s (install privileges confirmed)\n", pkgMgr)
	return pkgMgr, true
}

// detectPkgManager probes for common package managers in priority order.
func detectPkgManager(ctx context.Context, ex Executor) string {
	managers := []struct {
		name string
		cmd  string
	}{
		{"apt", "command -v apt-get 2>/dev/null"},
		{"dnf", "command -v dnf 2>/dev/null"},
		{"yum", "command -v yum 2>/dev/null"},
		{"brew", "command -v brew 2>/dev/null"},
	}
	for _, m := range managers {
		var b strings.Builder
		if err := ex.Run(ctx, m.cmd, &b); err == nil && strings.TrimSpace(b.String()) != "" {
			return m.name
		}
	}
	return "none"
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return s
}
