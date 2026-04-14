package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// RunNomadCLI runs the local nomad CLI with NOMAD_* values sourced from the
// active abc config context when available.
func RunNomadCLI(ctx context.Context, args []string, addr, token, region string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "nomad", args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	env := os.Environ()
	if addr == "" || token == "" || region == "" {
		if cfg, err := config.Load(); err == nil && cfg != nil {
			active := cfg.ActiveCtx()
			if addr == "" {
				addr = active.NomadAddr
			}
			if token == "" {
				token = active.NomadToken
			}
			if region == "" {
				region = active.Region
			}
		}
	}
	if addr != "" {
		env = append(env, "NOMAD_ADDR="+addr)
	}
	if token != "" {
		env = append(env, "NOMAD_TOKEN="+token)
	}
	if region != "" {
		env = append(env, "NOMAD_REGION="+region)
	}
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run nomad %v: %w", args, err)
	}
	return nil
}

// RunNomadCLIFromConfig runs the local nomad CLI using only the active abc
// config context for connection defaults.
func RunNomadCLIFromConfig(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunNomadCLI(ctx, args, "", "", "", stdin, stdout, stderr)
	}
