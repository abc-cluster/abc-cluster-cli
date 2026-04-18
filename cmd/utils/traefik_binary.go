package utils

import (
	"fmt"
	"os/exec"
	"strings"
)

// ResolveTraefikBinary returns a path to the traefik executable for subprocess use.
// It honours ABC_TRAEFIK_CLI_BINARY, TRAEFIK_CLI_BINARY, TRAEFIK_BINARY when set to an
// existing path, otherwise looks up "traefik" on PATH.
func ResolveTraefikBinary() (string, error) {
	if p := strings.TrimSpace(EnvOrDefault("ABC_TRAEFIK_CLI_BINARY", "TRAEFIK_CLI_BINARY", "TRAEFIK_BINARY")); p != "" {
		return p, nil
	}
	path, err := exec.LookPath("traefik")
	if err != nil {
		return "", fmt.Errorf("traefik not found on PATH (install traefik or set ABC_TRAEFIK_CLI_BINARY): %w", err)
	}
	return path, nil
}
