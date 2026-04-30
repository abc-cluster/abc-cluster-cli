package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

func newPushCmd() *cobra.Command {
	var dryRun bool
	var bucket string

	cmd := &cobra.Command{
		Use:   "push [tool[@version]]",
		Short: "Upload cached binaries to cluster S3 (always overwrites)",
		Long: `Upload all locally cached tool binaries to the cluster S3 bucket.

Credentials are read from the active context (admin.services.<context_service>).
abc tries the preferred service first (default: rustfs) and falls back to the
other if unreachable. The resolved endpoint is written back to tools.toml.

Push always overwrites the remote copy.

Examples:
  abc admin tools push            # push all cached binaries
  abc admin tools push s5cmd      # push s5cmd only
  abc admin tools push --dry-run  # show what would be pushed`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPush(cmd.Context(), cmd.OutOrStdout(), args, dryRun, bucket)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be pushed without uploading")
	cmd.Flags().StringVar(&bucket, "bucket", "", "Override bucket (default from tools.toml [push].bucket)")
	return cmd
}

func runPush(ctx context.Context, w io.Writer, args []string, dryRun bool, bucketOverride string) error {
	cfg, _, err := loadToolsConfig()
	if err != nil {
		return err
	}

	// ── Resolve S3 credentials from active context ────────────────────────────
	endpoint, envMap, err := resolveS3Backend(ctx, cfg)
	if err != nil {
		return err
	}

	bucket := cfg.Push.Bucket
	if bucketOverride != "" {
		bucket = bucketOverride
	}
	prefix := cfg.Push.Prefix

	activeCfg2, _ := config.Load()
	svcLabel := "rustfs"
	if activeCfg2 != nil {
		svcLabel = activeCfg2.ActiveCtx().ToolPushContextService()
	}
	fmt.Fprintf(w, "[abc] context_service: %s\n", svcLabel)
	fmt.Fprintf(w, "[abc] endpoint: %s\n", endpoint)
	fmt.Fprintf(w, "[abc] target: s3://%s/%s/\n\n", bucket, prefix)

	if dryRun {
		fmt.Fprintln(w, "[dry-run] no files will be uploaded")
	}

	// ── Ensure s5cmd is available for upload ──────────────────────────────────
	s5cmdBin, err := ensureS5cmd(ctx, w, dryRun)
	if err != nil {
		return fmt.Errorf("ensure s5cmd: %w", err)
	}

	// ── Collect binaries to push ──────────────────────────────────────────────
	binDir, err := utils.AssetDir()
	if err != nil {
		return err
	}

	filterName := ""
	if len(args) == 1 {
		parts := strings.SplitN(args[0], "@", 2)
		filterName = parts[0]
	}

	// Walk the flat binaries dir and pick files matching <tool>-<os>-<arch>.
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return fmt.Errorf("read binaries dir: %w", err)
	}

	type uploadTarget struct {
		localPath  string
		remoteKey  string // e.g. binary_tools/s5cmd-linux-amd64
		binaryName string // e.g. s5cmd-linux-amd64
	}

	var targets []uploadTarget
	for _, e := range entries {
		if e.IsDir() || e.Name() == "tools.toml" || e.Name() == "tools.toml.bak" {
			continue
		}
		name := e.Name()
		// Only push files that look like <tool>-<os>-<arch>.
		if !looksLikeCrossBinary(name, cfg) {
			continue
		}
		if filterName != "" && !strings.HasPrefix(name, filterName+"-") {
			continue
		}
		targets = append(targets, uploadTarget{
			localPath:  filepath.Join(binDir, name),
			remoteKey:  prefix + "/" + name,
			binaryName: name,
		})
	}

	// ── Collect locally built artifacts ──────────────────────────────────────────
	for _, loc := range cfg.EnabledLocals() {
		if filterName != "" && loc.Name != filterName {
			continue
		}
		if len(loc.Paths) > 0 {
			// Multi-arch binary: map of "os-arch" → filesystem path.
			// Sort keys for deterministic ordering.
			keys := make([]string, 0, len(loc.Paths))
			for k := range loc.Paths {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, key := range keys {
				localPath := loc.Paths[key]
				if info, err := os.Stat(localPath); err != nil || info.IsDir() || info.Size() == 0 {
					fmt.Fprintf(w, "  [local/%s] %-20s  ✗  not found: %s\n", loc.Name, key, localPath)
					continue
				}
				remoteName := loc.Name + "-" + key
				targets = append(targets, uploadTarget{
					localPath:  localPath,
					remoteKey:  prefix + "/" + remoteName,
					binaryName: "[local] " + remoteName,
				})
			}
		} else if loc.Path != "" {
			// Arch-agnostic artifact (e.g. JAR, wheel).
			localPath := loc.Path
			if info, err := os.Stat(localPath); err != nil || info.IsDir() || info.Size() == 0 {
				fmt.Fprintf(w, "  [local/%s]  ✗  not found: %s\n", loc.Name, localPath)
				continue
			}
			remoteName := filepath.Base(localPath)
			targets = append(targets, uploadTarget{
				localPath:  localPath,
				remoteKey:  prefix + "/" + remoteName,
				binaryName: "[local] " + remoteName,
			})
		}
	}

	if len(targets) == 0 {
		fmt.Fprintln(w, "Nothing to push — local cache is empty or no matching tools.")
		fmt.Fprintln(w, "Run: abc admin tools fetch")
		return nil
	}

	// ── Upload ────────────────────────────────────────────────────────────────
	uploaded := 0
	for _, t := range targets {
		remoteURI := fmt.Sprintf("s3://%s/%s", bucket, t.remoteKey)
		if dryRun {
			fmt.Fprintf(w, "  %-32s  ○  would upload → %s\n", t.binaryName, remoteURI)
			continue
		}

		args := []string{
			"--endpoint-url", endpoint,
			"cp", t.localPath, remoteURI,
		}
		if err := utils.RunExternalCLIWithEnv(
			ctx, args, s5cmdBin, []string{"s5cmd"},
			envMap,
			nil, w, w,
		); err != nil {
			fmt.Fprintf(w, "  %-32s  ✗  upload failed: %v\n", t.binaryName, err)
			continue
		}
		fmt.Fprintf(w, "  %-32s  ✓  uploaded → %s\n", t.binaryName, remoteURI)
		uploaded++
	}

	if dryRun {
		return nil
	}

	fmt.Fprintf(w, "\n%d/%d binaries pushed.\n", uploaded, len(targets))

	// Write the resolved endpoint back to config.yaml so job definitions can
	// reference it directly via abc config get.
	if err := writeEndpointToConfig(endpoint); err != nil {
		fmt.Fprintf(w, "[abc] warning: could not write endpoint to config.yaml: %v\n", err)
	} else {
		fmt.Fprintf(w, "[abc] endpoint written to config.yaml (admin.tools.endpoint): %s\n", endpoint)
	}

	fmt.Fprintf(w, "\nNomad artifact URL pattern:\n  %s/%s/<prefix>/<tool>-linux-${attr.cpu.arch}\n",
		endpoint, bucket)
	return nil
}

// ── S3 backend resolution ─────────────────────────────────────────────────────

// resolveS3Backend picks the reachable S3 service from the active context.
// Preference order: admin.tools.context_service → try the other service as fallback.
// Returns (endpoint, envMap, error).
func resolveS3Backend(ctx context.Context, cfg *ToolsConfig) (string, map[string]string, error) {
	activeCfg, err := config.Load()
	if err != nil {
		return "", nil, fmt.Errorf("load config: %w", err)
	}
	activeCtx := activeCfg.ActiveCtx()

	// context_service comes from config.yaml admin.tools.context_service.
	preferred := activeCtx.ToolPushContextService() // defaults to "rustfs"

	try := func(svc string) (string, map[string]string, bool) {
		var endpoint string
		var envMap map[string]string
		switch svc {
		case "rustfs":
			endpoint = activeCtx.RustfsS3APIEndpoint()
			envMap = activeCtx.AbcNodesRustfsStorageCLIEnv()
		case "minio":
			endpoint = activeCtx.MinioS3APIEndpoint()
			envMap = activeCtx.AbcNodesMinioStorageCLIEnv()
		default:
			return "", nil, false
		}
		endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if endpoint == "" {
			return "", nil, false
		}
		if !isEndpointReachable(ctx, endpoint) {
			return "", nil, false
		}
		return endpoint, envMap, true
	}

	if ep, env, ok := try(preferred); ok {
		return ep, env, nil
	}

	// Fallback.
	fallback := "minio"
	if preferred == "minio" {
		fallback = "rustfs"
	}
	if ep, env, ok := try(fallback); ok {
		return ep, env, nil
	}

	return "", nil, fmt.Errorf(
		"no reachable S3 endpoint found in active context (tried %s and %s).\n"+
			"Check admin.services.rustfs.endpoint / admin.services.minio.endpoint in your config.",
		preferred, fallback,
	)
}

// isEndpointReachable does a quick HTTP HEAD / GET with a short timeout.
func isEndpointReachable(ctx context.Context, endpoint string) bool {
	probe := strings.TrimRight(endpoint, "/")
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodHead, probe, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	// Any HTTP response (including 403, 404) means the endpoint is reachable.
	return true
}

// ── s5cmd for upload ──────────────────────────────────────────────────────────

// ensureS5cmd returns the path to s5cmd for the host platform.
// It checks the flat binary cache first (s5cmd-<os>-<arch> written by fetch),
// then falls back to the plain "s5cmd" managed path, then fetches via eget.
func ensureS5cmd(ctx context.Context, w io.Writer, dryRun bool) (string, error) {
	// 1. Arch-suffixed form in assets/ — written by `abc admin tools fetch`.
	assetDir, err := utils.AssetDir()
	if err != nil {
		return "", err
	}
	hostBin := "s5cmd-" + runtime.GOOS + "-" + runtime.GOARCH
	hostPath := filepath.Join(assetDir, hostBin)
	if info, err := os.Stat(hostPath); err == nil && !info.IsDir() && info.Size() > 0 {
		return hostPath, nil
	}

	// 2. Plain "s5cmd" in binaries/ — written by other abc admin commands.
	plainPath, err := utils.ManagedBinaryPath("s5cmd")
	if err == nil {
		if info, err := os.Stat(plainPath); err == nil && !info.IsDir() && info.Size() > 0 {
			return plainPath, nil
		}
	}

	if dryRun {
		return "(dry-run-s5cmd)", nil
	}

	// 3. Bootstrap via GitHub API + extraction into assets/ for the host arch.
	fmt.Fprintf(w, "[abc] s5cmd not in cache; fetching for host (%s/%s)...\n",
		runtime.GOOS, runtime.GOARCH)

	egetBin, err := utils.ManagedBinaryPath("eget")
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(egetBin); err != nil {
		res, err := utils.SetupEgetBinary(w)
		if err != nil {
			return "", fmt.Errorf("bootstrap eget for s5cmd fetch: %w", err)
		}
		egetBin = res.Path
	}
	_ = egetBin // eget bootstrapped; fetchBinary uses GitHub API directly

	if err := fetchBinary(ctx, "peak", "s5cmd", "latest",
		runtime.GOOS, runtime.GOARCH, "s5cmd", hostPath); err != nil {
		return "", fmt.Errorf("fetch s5cmd: %w", err)
	}
	fmt.Fprintf(w, "[abc] s5cmd → %s\n", hostPath)
	return hostPath, nil
}

// writeEndpointToConfig persists the resolved S3 endpoint to config.yaml
// under admin.tools.endpoint of the active context.
func writeEndpointToConfig(endpoint string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	activeCtx := cfg.ActiveCtx()
	// Only write back if the value changed.
	if activeCtx.ToolPushEndpoint() == endpoint {
		return nil
	}
	// Resolve the canonical context name from the active context name.
	activeContextName := cfg.ActiveContext
	key := "contexts." + activeContextName + ".admin.tools.endpoint"
	if err := cfg.Set(key, endpoint); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	return cfg.Save()
}

// looksLikeCrossBinary reports whether a filename looks like <tool>-<os>-<arch>
// where <tool> is one of the enabled tools in the config.
func looksLikeCrossBinary(filename string, cfg *ToolsConfig) bool {
	for _, t := range cfg.EnabledTools() {
		if strings.HasPrefix(filename, t.Name+"-") {
			return true
		}
	}
	return false
}

// findS5cmdBin locates a usable s5cmd binary without downloading anything.
// Checks assets/ (arch-suffixed, preferred) then binaries/ (plain fallback).
// Used by both push and list to avoid duplicating the lookup logic.
func findS5cmdBin() (string, bool) {
	if assetDir, err := utils.AssetDir(); err == nil {
		p := filepath.Join(assetDir, "s5cmd-"+runtime.GOOS+"-"+runtime.GOARCH)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() > 0 {
			return p, true
		}
	}
	if p, err := utils.ManagedBinaryPath("s5cmd"); err == nil {
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Size() > 0 {
			return p, true
		}
	}
	return "", false
}

