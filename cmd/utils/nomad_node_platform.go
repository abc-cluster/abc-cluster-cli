package utils

import (
	"fmt"
	"strings"
)

// NomadNodeReleasePlatform maps Nomad node fingerprints to GOOS/GOARCH used by
// release artifacts (linux/amd64, darwin/arm64, windows/amd64, …). It does not
// preserve distribution-specific os.name values such as "ubuntu" or "amzn".
func NomadNodeReleasePlatform(node *NomadNode) (goos, goarch string, err error) {
	if node == nil {
		return "", "", fmt.Errorf("nil node")
	}
	if node.Attributes == nil {
		return "", "", fmt.Errorf("node %q has no Nomad fingerprint attributes", node.Name)
	}

	goos, err = nomadFingerprintToReleaseGOOS(
		node.Attributes["os.name"],
		node.Attributes["kernel.name"],
	)
	if err != nil {
		return "", "", fmt.Errorf("node %q: %w", node.Name, err)
	}

	goarch = strings.TrimSpace(strings.ToLower(node.Attributes["cpu.arch"]))
	if goarch == "" {
		return "", "", fmt.Errorf("node %q: missing cpu.arch (pass --platform=os/arch)", node.Name)
	}

	goos = normalizeGOOS(goos)
	goarch = normalizeGOARCH(goarch)
	return goos, goarch, nil
}

// nomadFingerprintToReleaseGOOS maps Nomad os.name / kernel.name to a single
// Go-style OS string for release selection (linux, darwin, windows, …).
//
// On Linux hosts, Nomad typically sets kernel.name=linux and os.name to the
// distribution label; we always collapse distro labels to "linux" so asset
// names match abc-node-probe-linux-amd64 style binaries.
func nomadFingerprintToReleaseGOOS(osName, kernelName string) (string, error) {
	k := strings.TrimSpace(strings.ToLower(kernelName))
	o := strings.TrimSpace(strings.ToLower(osName))

	if k == "linux" || k == "windows" || k == "darwin" {
		return k, nil
	}
	if k == "freebsd" || k == "openbsd" || k == "netbsd" {
		return k, nil
	}
	if k != "" {
		return "", fmt.Errorf("unsupported kernel.name %q (use --platform=os/arch)", strings.TrimSpace(kernelName))
	}

	// kernel.name missing — infer from os.name only.
	if o == "" {
		return "", fmt.Errorf("missing os.name and kernel.name for OS inference")
	}
	if o == "linux" {
		return "linux", nil
	}
	switch normalizeGOOS(o) {
	case "darwin", "windows":
		return normalizeGOOS(o), nil
	}
	if strings.Contains(o, "windows") || strings.Contains(o, "microsoft") {
		return "windows", nil
	}
	if strings.Contains(o, "darwin") || o == "macos" || o == "osx" {
		return "darwin", nil
	}
	if o == "freebsd" || o == "openbsd" || o == "netbsd" {
		return o, nil
	}
	// Typical Linux clients: os.name is the distro (ubuntu, debian, amzn, …).
	return "linux", nil
}
