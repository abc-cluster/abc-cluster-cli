package node

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"
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
//
// sudoPassword, when non-empty, tells preflight that the SSH user has password-
// based sudo access. The passwordless check (sudo -n) is skipped and HasSudo is
// set to true; the actual commands will use sudo -S (injected by sshExec.Run).
//
// requirePkgManagerCheck should be true only when the selected install method
// requires package-manager privileges.
func RunPreflight(ctx context.Context, ex Executor, w io.Writer, sudoPassword string, requirePkgManagerCheck bool) (*PreflightResult, error) {
	log := debuglog.FromContext(ctx)
	res := &PreflightResult{
		OS:   ex.OS(),
		Arch: ex.Arch(),
	}

	fmt.Fprintf(w, "\n  Preflight:\n")
	fmt.Fprintf(w, "    ✓ OS              %s/%s\n", res.OS, res.Arch)
	log.LogAttrs(ctx, debuglog.L1, "preflight.check",
		debuglog.AttrsPreflight("os_detection", true, res.OS+"/"+res.Arch, 0)...,
	)

	// Init system
	t := time.Now()
	switch res.OS {
	case "linux":
		res.InitSystem = detectLinuxInitSystem(ctx, ex)
		if res.InitSystem == "systemd" {
			res.InitSystem = "systemd"
			fmt.Fprintf(w, "    ✓ Init system     systemd\n")
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("init_system", true, "systemd", time.Since(t).Milliseconds())...,
			)
		} else {
			fmt.Fprintf(w, "    ✗ Init system     %q (not systemd — service registration unavailable)\n", res.InitSystem)
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("init_system", false, res.InitSystem, time.Since(t).Milliseconds())...,
			)
		}
	case "darwin":
		res.InitSystem = "launchd"
		fmt.Fprintf(w, "    ✓ Init system     launchd\n")
		log.LogAttrs(ctx, debuglog.L1, "preflight.check",
			debuglog.AttrsPreflight("init_system", true, "launchd", time.Since(t).Milliseconds())...,
		)
	default:
		res.InitSystem = "none"
		fmt.Fprintf(w, "    - Init system     manual (Windows — sc.exe instructions will be printed)\n")
		log.LogAttrs(ctx, debuglog.L1, "preflight.check",
			debuglog.AttrsPreflight("init_system", true, "none (windows)", time.Since(t).Milliseconds())...,
		)
	}

	// Sudo / admin access
	t = time.Now()
	if res.OS == "windows" {
		var b strings.Builder
		_ = ex.Run(ctx, "whoami /groups 2>&1", &b)
		// S-1-16-12288 is the High Mandatory Level SID (Administrator)
		if strings.Contains(b.String(), "S-1-16-12288") {
			res.HasSudo = true
			fmt.Fprintf(w, "    ✓ Sudo access     ok (Administrator)\n")
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("sudo_access", true, "Administrator", time.Since(t).Milliseconds())...,
			)
		} else {
			fmt.Fprintf(w, "    ✗ Sudo access     not Administrator — re-run as Administrator\n")
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("sudo_access", false, "not Administrator", time.Since(t).Milliseconds())...,
			)
		}
	} else if sudoPassword != "" {
		// Password-based sudo: skip the passwordless check and trust the password.
		// sshExec.Run() will inject it via sudo -S on every privileged command.
		res.HasSudo = true
		fmt.Fprintf(w, "    ✓ Sudo access     ok (password auth)\n")
		log.LogAttrs(ctx, debuglog.L1, "preflight.check",
			debuglog.AttrsPreflight("sudo_access", true, "password auth", time.Since(t).Milliseconds())...,
		)
	} else {
		if err := ex.Run(ctx, "sudo -n true 2>/dev/null", io.Discard); err == nil {
			res.HasSudo = true
			fmt.Fprintf(w, "    ✓ Sudo access     ok (passwordless)\n")
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("sudo_access", true, "passwordless", time.Since(t).Milliseconds())...,
			)
		} else {
			fmt.Fprintf(w, "    ✗ Sudo access     sudo required\n")
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("sudo_access", false, "sudo -n failed", time.Since(t).Milliseconds())...,
			)
		}
	}

	// Package manager + install permission check
	// We only need this when Nomad is not already installed (curl + unzip must be present,
	// and the user must be able to write to /usr/local/bin via sudo).
	// The check is purely informational on Windows — we install via direct binary upload.
	t = time.Now()
	if res.OS != "windows" {
		if requirePkgManagerCheck {
			res.PkgManager, res.CanInstallPkgs = checkPackageAccess(ctx, ex, w, res.HasSudo)
		} else {
			res.PkgManager = detectPkgManager(ctx, ex)
			res.CanInstallPkgs = true
			if res.PkgManager == "none" {
				fmt.Fprintf(w, "    - Pkg manager     not detected (not required for static-binary install)\n")
			} else {
				fmt.Fprintf(w, "    ✓ Pkg manager     %s (privilege check skipped)\n", res.PkgManager)
			}
		}
		log.LogAttrs(ctx, debuglog.L1, "preflight.check",
			debuglog.AttrsPreflight("pkg_manager", res.CanInstallPkgs, res.PkgManager, time.Since(t).Milliseconds())...,
		)
	} else {
		res.PkgManager = "none"
		res.CanInstallPkgs = res.HasSudo
	}

	// Nomad already installed?
	t = time.Now()
	{
		var b strings.Builder
		err := ex.Run(ctx, "command -v nomad 2>/dev/null", &b)
		if err == nil && strings.TrimSpace(b.String()) != "" {
			res.NomadInstalled = true
			fmt.Fprintf(w, "    ✓ Nomad           already installed (will skip Nomad install)\n")
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("nomad_installed", true, strings.TrimSpace(b.String()), time.Since(t).Milliseconds())...,
			)
		} else {
			fmt.Fprintf(w, "    - Nomad           not installed\n")
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("nomad_installed", false, "", time.Since(t).Milliseconds())...,
			)
		}
	}

	// Tailscale already installed?
	t = time.Now()
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
				log.LogAttrs(ctx, debuglog.L1, "preflight.check",
					debuglog.AttrsPreflight("tailscale", true, "installed+connected", time.Since(t).Milliseconds())...,
				)
			} else {
				fmt.Fprintf(w, "    ✓ Tailscale       installed (will run tailscale up)\n")
				log.LogAttrs(ctx, debuglog.L1, "preflight.check",
					debuglog.AttrsPreflight("tailscale", true, "installed (not connected)", time.Since(t).Milliseconds())...,
				)
			}
		} else {
			fmt.Fprintf(w, "    - Tailscale       not installed\n")
			log.LogAttrs(ctx, debuglog.L1, "preflight.check",
				debuglog.AttrsPreflight("tailscale", false, "not installed", time.Since(t).Milliseconds())...,
			)
		}
	}

	// Log overall preflight summary before hard stops.
	log.LogAttrs(ctx, debuglog.L1, "preflight.complete",
		slog.String("op", "node.add"),
		slog.String("pkg_manager", res.PkgManager),
		slog.Bool("has_sudo", res.HasSudo),
		slog.Bool("nomad_installed", res.NomadInstalled),
		slog.Bool("tailscale_installed", res.TailscaleInstalled),
	)

	// Hard stops
	if !res.HasSudo && res.OS != "windows" {
		return res, fmt.Errorf(`sudo access required on %s — aborting

  The SSH user lacks passwordless sudo. To fix this, pick one option:
    1. Supply the user's sudo password:
         abc node add --remote=<host> --password=<pass>
         (or set ABC_NODE_PASSWORD=<pass> in the environment)
    2. Add the user to sudoers for passwordless access on the remote host:
         echo "<user> ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/<user>
    3. Connect as root:  abc node add --remote=<host> --user=root
    4. Use --skip-preflight if you have already configured sudo`, res.OS)
	}
	if res.OS == "linux" && res.InitSystem != "systemd" {
		return res, fmt.Errorf(`systemd required on Linux for automatic service registration (found init: %q)

  Options:
    • Use --skip-enable --skip-start to install the binary and config only
    • Start Nomad manually: sudo /usr/local/bin/nomad agent -config /etc/nomad.d
    • Use --skip-preflight to bypass this check if you know what you are doing`, res.InitSystem)
	}
	if requirePkgManagerCheck && !res.CanInstallPkgs && res.OS != "windows" {
		// Non-fatal warning — keep going, but highlight that package-manager install
		// steps may fail if privileges are insufficient.
		fmt.Fprintf(w, "\n  Warning: Could not verify package-install privileges.\n")
		fmt.Fprintf(w, "  Selected install method uses package-manager installation.\n")
		fmt.Fprintf(w, "  If installation fails with permission errors, verify sudo access.\n")
	}

	return res, nil
}

// checkPackageAccess detects the available package manager and verifies the user
// can run privileged install commands. Returns (pkgManager, canInstall).
//
// This check catches permission issues early when package-manager installation
// is selected for Nomad and/or Tailscale.
func checkPackageAccess(ctx context.Context, ex Executor, w io.Writer, hasSudo bool) (pkgMgr string, canInstall bool) {
	// Detect package manager
	pkgMgr = detectPkgManager(ctx, ex)

	if pkgMgr == "none" {
		fmt.Fprintf(w, "    ? Pkg manager     none detected (Nomad installed via direct binary upload)\n")
		return pkgMgr, hasSudo // binary upload only requires sudo for write to /usr/local/bin
	}

	if !hasSudo {
		fmt.Fprintf(w, "    ✗ Pkg manager     %s detected but user lacks sudo — package-manager install may fail\n", pkgMgr)
		fmt.Fprintf(w, "                      Tip: ensure the SSH user has NOPASSWD sudo or switch to --package-install-method=static\n")
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
		cmds []string
	}{
		{
			name: "apt",
			cmds: []string{
				"command -v apt-get >/dev/null 2>&1",
				"test -x /usr/bin/apt-get",
				"test -x /bin/apt-get",
			},
		},
		{
			name: "dnf",
			cmds: []string{
				"command -v dnf >/dev/null 2>&1",
				"test -x /usr/bin/dnf",
				"test -x /bin/dnf",
			},
		},
		{
			name: "yum",
			cmds: []string{
				"command -v yum >/dev/null 2>&1",
				"test -x /usr/bin/yum",
				"test -x /bin/yum",
			},
		},
		{
			name: "brew",
			cmds: []string{
				"command -v brew >/dev/null 2>&1",
				"test -x /usr/local/bin/brew",
				"test -x /opt/homebrew/bin/brew",
			},
		},
	}
	for _, m := range managers {
		for _, cmd := range m.cmds {
			if err := ex.Run(ctx, cmd, io.Discard); err == nil {
				return m.name
			}
		}
	}
	return "none"
}

// detectLinuxInitSystem tries multiple signals in order:
//  1. PID 1 command name (/proc/1/comm, then ps fallback)
//  2. systemd runtime marker (/run/systemd/system)
//  3. systemd binary locations (/usr/bin/systemd, /lib/systemd/systemd)
//  4. systemctl presence in PATH
//
// Fallback checks are only used when PID 1 could not be determined (empty output),
// which avoids misclassifying non-systemd hosts that merely have systemd binaries.
func detectLinuxInitSystem(ctx context.Context, ex Executor) string {
	proc1 := readFirstLine(ctx, ex, "cat /proc/1/comm 2>/dev/null")
	if proc1 == "" {
		proc1 = readFirstLine(ctx, ex, "ps -p 1 -o comm= 2>/dev/null")
	}
	if proc1 != "" {
		if strings.Contains(strings.ToLower(proc1), "systemd") {
			return "systemd"
		}
		return proc1
	}

	if err := ex.Run(ctx, "test -d /run/systemd/system", io.Discard); err == nil {
		return "systemd"
	}
	if err := ex.Run(ctx, "test -x /usr/bin/systemd || test -x /lib/systemd/systemd", io.Discard); err == nil {
		return "systemd"
	}
	if err := ex.Run(ctx, "command -v systemctl >/dev/null 2>&1", io.Discard); err == nil {
		return "systemd"
	}
	return "unknown"
}

func readFirstLine(ctx context.Context, ex Executor, cmd string) string {
	var b strings.Builder
	if err := ex.Run(ctx, cmd, &b); err != nil {
		return ""
	}
	return firstLine(strings.TrimSpace(b.String()))
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
