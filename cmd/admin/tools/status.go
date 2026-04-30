package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Quick health check — exits 1 if anything is missing",
		Long: `Report how many tool binaries are in sync across local cache and remote.

Exits 0 when everything expected by tools.toml is both fetched and pushed.
Exits 1 otherwise, with a summary of what is missing.

Useful as a pre-flight gate before submitting Nomad jobs:
  abc admin tools status || exit 1
  abc job run my-job.nomad.hcl`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func runStatus(ctx context.Context, w io.Writer) error {
	cfg, _, err := loadToolsConfig()
	if err != nil {
		return err
	}

	arches := activeArchitectures(cfg)
	tools := cfg.EnabledTools()

	total := len(tools) * len(arches)

	// Count local.
	binDir, err := utils.AssetDir()
	if err != nil {
		return err
	}

	missingLocal := []string{}
	for _, t := range tools {
		for _, osArch := range arches {
			parts := strings.SplitN(osArch, "/", 2)
			if len(parts) != 2 {
				continue
			}
			goos, goarch := parts[0], parts[1]
			binaryName := t.Name + "-" + goos + "-" + goarch
			localPath := filepath.Join(binDir, binaryName)
			info, err := os.Stat(localPath)
			if err != nil || info.IsDir() || info.Size() == 0 {
				missingLocal = append(missingLocal, binaryName)
			}
		}
	}

	// Count remote (best-effort; skip if no endpoint).
	// Prefer admin.tools.endpoint from config.yaml (written back by push).
	endpoint := ""
	if activeCtx, err := loadConfig(); err == nil {
		endpoint = activeCtx.ToolPushEndpoint()
	}
	if endpoint == "" {
		ep, _, resolveErr := resolveS3Backend(ctx, cfg)
		if resolveErr == nil {
			endpoint = ep
		}
	}

	missingRemote := []string{}
	if endpoint != "" {
		remoteFiles := listRemoteFiles(ctx, cfg, endpoint)
		for _, t := range tools {
			for _, osArch := range arches {
				parts := strings.SplitN(osArch, "/", 2)
				if len(parts) != 2 {
					continue
				}
				goos, goarch := parts[0], parts[1]
				binaryName := t.Name + "-" + goos + "-" + goarch
				if !remoteFiles[binaryName] {
					missingRemote = append(missingRemote, binaryName)
				}
			}
		}
	}

	// Print summary.
	fmt.Fprintf(w, "\ntools.toml: %d tool(s) · %d arch(es) → %d binaries expected\n",
		len(tools), len(arches), total)

	svcLabel := "rustfs"
	if activeCtx, err := loadConfig(); err == nil {
		svcLabel = activeCtx.ToolPushContextService()
	}
	if endpoint != "" {
		fmt.Fprintf(w, "Context: %s  →  %s\n\n", svcLabel, endpoint)
	} else {
		fmt.Fprintf(w, "Remote: (no endpoint configured — run abc admin tools push first)\n\n")
	}

	inSync := total - len(missingLocal)
	if inSync < 0 {
		inSync = 0
	}

	if len(missingLocal) == 0 && len(missingRemote) == 0 {
		fmt.Fprintf(w, "  ✓  %d/%d in sync\n\n", total, total)
		return nil
	}

	if inSync > 0 {
		fmt.Fprintf(w, "  ✓  %d in sync\n", inSync)
	}
	if len(missingLocal) > 0 {
		toolNames := uniqueToolNames(missingLocal)
		fmt.Fprintf(w, "  ✗  %d not fetched locally   →  abc admin tools fetch %s\n",
			len(missingLocal), strings.Join(toolNames, " "))
	}
	if len(missingRemote) > 0 {
		toolNames := uniqueToolNames(missingRemote)
		fmt.Fprintf(w, "  ✗  %d not on remote         →  abc admin tools push %s\n",
			len(missingRemote), strings.Join(toolNames, " "))
	}
	fmt.Fprintln(w)

	return fmt.Errorf("tools not in sync")
}

// uniqueToolNames extracts the tool name prefix from binary names like "s5cmd-linux-amd64".
func uniqueToolNames(binaries []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range binaries {
		name := strings.SplitN(b, "-", 2)[0]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
