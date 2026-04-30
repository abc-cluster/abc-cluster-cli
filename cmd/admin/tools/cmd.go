// Package tools implements the "abc admin tools" subcommand group.
//
// These commands manage the operator-side binary cache (~/.abc/binaries/) and
// push cluster-node tools to the shared S3 bucket so Nomad jobs can fetch them
// at runtime without depending on GitHub or requiring pre-built images.
//
// Workflow:
//
//	abc admin tools init     # create ~/.abc/binaries/tools.toml from bundled default
//	abc admin tools edit     # open tools.toml in $EDITOR
//	abc admin tools fetch    # download all tools for all configured architectures
//	abc admin tools push     # upload cached binaries to cluster S3
//	abc admin tools list     # show local cache vs remote state
//	abc admin tools status   # quick health check (exit 1 if anything missing)
package tools

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

//go:embed default_tools.toml
var defaultToolsTOML []byte

// toolsConfigPath returns the canonical path for tools.toml (~/.abc/assets/tools.toml).
func toolsConfigPath() (string, error) {
	dir, err := utils.AssetDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tools.toml"), nil
}

// loadToolsConfig reads tools.toml from the asset dir.
// On first run after an upgrade it automatically migrates tools.toml and all
// arch-suffixed binaries from the old ~/.abc/binaries/ location to ~/.abc/assets/.
func loadToolsConfig() (*ToolsConfig, string, error) {
	path, err := toolsConfigPath()
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		migrated, migErr := migrateAssetsFromBinaries(path)
		if migErr != nil {
			fmt.Fprintf(os.Stderr, "[abc] warning: migration failed: %v\n", migErr)
		}
		if !migrated {
			return nil, path, fmt.Errorf(
				"tools.toml not found at %s\nRun: abc admin tools init", path)
		}
	}
	cfg, err := ReadToolsConfig(path)
	return cfg, path, err
}

// migrateAssetsFromBinaries moves tools.toml and arch-suffixed artifacts from
// the old ~/.abc/binaries/ layout to ~/.abc/assets/.  Plain-named host
// binaries (eget, mc, nomad, …) are left in place.
// Returns true if tools.toml now exists at newTomlPath.
func migrateAssetsFromBinaries(newTomlPath string) (bool, error) {
	oldBinDir, err := utils.ManagedBinaryDir()
	if err != nil {
		return false, err
	}
	oldTomlPath := filepath.Join(oldBinDir, "tools.toml")
	if _, err := os.Stat(oldTomlPath); os.IsNotExist(err) {
		return false, nil // nothing to migrate
	}

	assetDir := filepath.Dir(newTomlPath)
	fmt.Fprintf(os.Stderr, "[abc] migrating tools from %s → %s\n", oldBinDir, assetDir)

	// Move tools.toml first.
	if err := moveFile(oldTomlPath, newTomlPath); err != nil {
		return false, fmt.Errorf("move tools.toml: %w", err)
	}

	// Move arch-suffixed artifacts and sidecars; leave plain-named host tools.
	entries, _ := os.ReadDir(oldBinDir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isAssetFile(e.Name()) {
			src := filepath.Join(oldBinDir, e.Name())
			dst := filepath.Join(assetDir, e.Name())
			_ = moveFile(src, dst) // best-effort; log nothing on partial failure
		}
	}
	return true, nil
}

// isAssetFile reports whether a filename belongs in ~/.abc/assets/ rather than
// ~/.abc/binaries/. Assets are arch-suffixed binaries and non-executable
// distribution artifacts.
func isAssetFile(name string) bool {
	lower := strings.ToLower(name)
	for _, tok := range []string{"-linux-", "-darwin-", "-windows-", "-freebsd-"} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	for _, ext := range []string{".jar", ".bz2"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return name == "tools.toml.bak"
}

// moveFile moves src to dst, falling back to copy+delete across filesystems.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return err
	}
	return os.Remove(src)
}

// NewCmd returns the "tools" subcommand group under "abc admin".
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Manage cluster-node tool binaries (fetch, push, list)",
		Long: `Download and distribute tool binaries for cluster nodes.

  abc admin tools init     Create ~/.abc/assets/tools.toml from the bundled default
  abc admin tools edit     Open tools.toml in $EDITOR
  abc admin tools fetch    Download tools for all configured architectures
  abc admin tools push     Upload cached binaries to cluster S3
  abc admin tools list         Show local cache vs remote state side-by-side
  abc admin tools status       Quick health check; exits 1 if anything is missing
  abc admin tools artifact-url Print Nomad artifact stanza for a tool

Directory layout:
  ~/.abc/assets/           Distribution artifacts — arch-suffixed binaries, JARs,
                           tools.toml. Everything fetched here is pushed to S3.
  ~/.abc/binaries/         Host-platform executables (eget, mc, nomad, …).
                           Safe to add to $PATH. Not scanned by push.

Remote:  s3://<bucket>/<prefix>/<tool>-<os>-<arch>

Target architectures come from admin.tools.architectures in the active context
(default: linux/amd64, linux/arm64).`,
	}

	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newEditCmd())
	cmd.AddCommand(newFetchCmd())
	cmd.AddCommand(newPushCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newArtifactURLCmd())

	return cmd
}
