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
func RunNomadCLI(ctx context.Context, args []string, binaryLocation, addr, token, region string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runConfiguredNomadToolCLI(ctx, "nomad", args, binaryLocation, addr, token, region, stdin, stdout, stderr)
}

// RunNomadCLIFromConfig runs the local nomad CLI using only the active abc
// config context for connection defaults.
func RunNomadCLIFromConfig(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunNomadCLI(ctx, args, "", "", "", "", stdin, stdout, stderr)
}

// RunNomadPackCLI runs the local nomad-pack CLI with NOMAD_* values sourced
// from the active abc config context when available.
func RunNomadPackCLI(ctx context.Context, args []string, binaryLocation string, stdin io.Reader, stdout, stderr io.Writer) error {
	if binaryLocation == "" {
		binaryLocation = EnvOrDefault(
			"ABC_NOMAD_PACK_CLI_BINARY",
			"NOMAD_PACK_CLI_BINARY",
			"NOMAD_PACK_BINARY",
		)
		if binaryLocation == "" {
			if managedPath, mErr := ManagedBinaryPath("nomad-pack"); mErr == nil {
				if info, sErr := os.Stat(managedPath); sErr == nil && !info.IsDir() {
					binaryLocation = managedPath
				}
			}
		}
	}

	return runConfiguredNomadToolCLI(ctx, "nomad-pack", args, binaryLocation, "", "", "", stdin, stdout, stderr)
}

func runConfiguredNomadToolCLI(ctx context.Context, binaryName string, args []string, binaryLocation, addr, token, region string, stdin io.Reader, stdout, stderr io.Writer) error {
	binary := binaryLocation
	if binary == "" {
		binary = binaryName
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	cfg, _ := config.Load()
	if cfg != nil {
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
	if addr != "" {
		addr = NormalizeNomadAPIAddr(WithDefaultNomadHTTPPort(addr))
	}
	base := os.Environ()
	base = upsertEnv(base, map[string]string{
		"NOMAD_ADDR":   addr,
		"NOMAD_TOKEN":  token,
		"NOMAD_REGION": region,
	})
	if cfg != nil {
		if ns := cfg.ActiveCtx().AbcNodesNomadNamespaceForCLI(); ns != "" {
			base = UpsertEnvOnlyMissing(base, map[string]string{"NOMAD_NAMESPACE": ns})
		}
	}
	cmd.Env = base

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s %v: %w", binary, args, err)
	}
	return nil
}
