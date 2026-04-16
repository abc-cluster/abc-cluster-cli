package utils

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// RunNomadCLI runs the local nomad CLI with NOMAD_* values sourced from the
// active abc config context when available.
func RunNomadCLI(ctx context.Context, args []string, binaryLocation, addr, token, region string, stdin io.Reader, stdout, stderr io.Writer) error {
	binary := binaryLocation
	if binary == "" {
		binary = "nomad"
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if addr == "" || token == "" || region == "" {
		if cfg, err := config.Load(); err == nil && cfg != nil {
			active := cfg.ActiveCtx()
			if addr == "" {
				addr = active.NomadAddr()
			}
			if token == "" {
				token = active.NomadToken()
			}
			if region == "" {
				region = active.NomadRegion()
			}
		}
	}
	if addr != "" {
		addr = NormalizeNomadAPIAddr(addr)
	}
	cmd.Env = upsertEnv(cmd.Environ(), map[string]string{
		"NOMAD_ADDR":   addr,
		"NOMAD_TOKEN":  token,
		"NOMAD_REGION": region,
	})

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s %v: %w", binary, args, err)
	}
	return nil
}

// RunNomadCLIFromConfig runs the local nomad CLI using only the active abc
// config context for connection defaults.
func RunNomadCLIFromConfig(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunNomadCLI(ctx, args, "", "", "", "", stdin, stdout, stderr)
}
