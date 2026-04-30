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

func newListCmd() *cobra.Command {
	var localOnly bool
	var remoteOnly bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show local cache vs remote state side-by-side",
		Long: `List all tool binaries expected by tools.toml, showing which are
present locally (~/.abc/binaries/) and which are on remote cluster S3.

  --local    Only check local cache (faster, no network)
  --remote   Only check remote (no local scan)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd.Context(), cmd.OutOrStdout(), localOnly, remoteOnly)
		},
	}

	cmd.Flags().BoolVar(&localOnly, "local", false, "Skip remote check")
	cmd.Flags().BoolVar(&remoteOnly, "remote", false, "Skip local scan")
	return cmd
}

func runList(ctx context.Context, w io.Writer, localOnly, remoteOnly bool) error {
	cfg, _, err := loadToolsConfig()
	if err != nil {
		return err
	}

	binDir, err := utils.AssetDir()
	if err != nil {
		return err
	}

	// Resolve S3 info for header (don't fail if unreachable — list works locally).
	// Prefer admin.tools.endpoint from config.yaml (written back by push).
	endpoint := ""
	if activeCtx, err := loadConfig(); err == nil {
		endpoint = activeCtx.ToolPushEndpoint()
	}
	if endpoint == "" && !localOnly {
		ep, _, resolveErr := resolveS3Backend(ctx, cfg)
		if resolveErr == nil {
			endpoint = ep
		}
	}

	// Build the set of files on remote (via s5cmd ls), if requested.
	remoteFiles := map[string]bool{}
	if !localOnly && endpoint != "" {
		remoteFiles = listRemoteFiles(ctx, cfg, endpoint)
	}

	// Print header.
	svcLabel := "rustfs"
	if activeCtx, err := loadConfig(); err == nil {
		svcLabel = activeCtx.ToolPushContextService()
	}
	contextLabel := svcLabel
	if endpoint != "" {
		contextLabel = fmt.Sprintf("%s  |  %s/%s/%s/", svcLabel, endpoint, cfg.Push.Bucket, cfg.Push.Prefix)
	}
	fmt.Fprintf(w, "\nContext: %s\n\n", contextLabel)

	header := fmt.Sprintf("%-32s  %-8s  %-8s", "BINARY", "LOCAL", "REMOTE")
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, strings.Repeat("─", len(header)))

	missingLocal := 0
	missingRemote := 0

	// For each expected binary (enabled tools × configured arches):
	arches := activeArchitectures(cfg)
	for _, t := range cfg.EnabledTools() {
		for _, osArch := range arches {
			parts := strings.SplitN(osArch, "/", 2)
			if len(parts) != 2 {
				continue
			}
			goos, goarch := parts[0], parts[1]
			binaryName := t.Name + "-" + goos + "-" + goarch
			localPath := filepath.Join(binDir, binaryName)

			localMark := "✗"
			if !remoteOnly {
				if info, err := os.Stat(localPath); err == nil && !info.IsDir() && info.Size() > 0 {
					localMark = "✓"
				} else {
					missingLocal++
				}
			} else {
				localMark = "-"
			}

			remoteMark := "-"
			note := ""
			if !localOnly {
				if endpoint == "" {
					remoteMark = "?"
					note = "  ← no endpoint configured"
				} else if remoteFiles[binaryName] {
					remoteMark = "✓"
				} else {
					remoteMark = "✗"
					missingRemote++
					note = "  ← not pushed"
					if localMark == "✗" {
						note = "  ← not fetched"
					}
				}
			}

			fmt.Fprintf(w, "%-32s  %-8s  %-8s%s\n", binaryName, localMark, remoteMark, note)
		}
	}

	fmt.Fprintln(w)
	if missingLocal > 0 {
		fmt.Fprintf(w, "  %d not fetched locally  →  abc admin tools fetch\n", missingLocal)
	}
	if missingRemote > 0 {
		fmt.Fprintf(w, "  %d not on remote        →  abc admin tools push\n", missingRemote)
	}
	if missingLocal == 0 && missingRemote == 0 {
		fmt.Fprintln(w, "  All binaries in sync ✓")
	}
	return nil
}

// listRemoteFiles calls s5cmd ls to enumerate binaries in the remote prefix.
// Returns a set of base filenames present on remote.
func listRemoteFiles(ctx context.Context, cfg *ToolsConfig, endpoint string) map[string]bool {
	s5cmdBin, ok := findS5cmdBin()
	if !ok {
		return nil
	}

	_, envMap, err := resolveS3Backend(ctx, cfg)
	if err != nil {
		return nil
	}

	remotePrefix := fmt.Sprintf("s3://%s/%s/", cfg.Push.Bucket, cfg.Push.Prefix)
	args := []string{"--endpoint-url", endpoint, "ls", remotePrefix}

	var sb strings.Builder
	_ = utils.RunExternalCLIWithEnv(ctx, args, s5cmdBin, []string{"s5cmd"}, envMap, nil, &sb, io.Discard)

	found := map[string]bool{}
	for _, line := range strings.Split(sb.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// s5cmd ls output: "2024/01/01 00:00:00      8765432 s5cmd-linux-amd64"
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			found[fields[3]] = true
		} else if len(fields) >= 1 {
			found[fields[len(fields)-1]] = true
		}
	}
	return found
}

// activeArchitectures returns arches from context config, falling back to default.
func activeArchitectures(cfg *ToolsConfig) []string {
	// Try loading context config to get admin.tools.architectures.
	activeCfg, err := loadConfig()
	if err == nil {
		arches := activeCfg.ToolArchitectures()
		if len(arches) > 0 {
			return arches
		}
	}
	// Fall back to a fixed default so list works without a context.
	return []string{"linux/amd64", "linux/arm64"}
}
