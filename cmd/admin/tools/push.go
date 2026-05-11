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
	"github.com/abc-cluster/abc-cluster-cli/internal/wave"
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
			// Remote key uses the "<name>-any" convention so artifact-url can
			// produce a consistent, predictable URL without arch interpolation.
			localPath := loc.Path
			if info, err := os.Stat(localPath); err != nil || info.IsDir() || info.Size() == 0 {
				fmt.Fprintf(w, "  [local/%s]  ✗  not found: %s\n", loc.Name, localPath)
				continue
			}
			remoteName := loc.Name + "-any"
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

	// ── Ensure bucket exists (idempotent create) ──────────────────────────────
	if !dryRun {
		if err := ensureBucket(ctx, w, s5cmdBin, endpoint, envMap, bucket); err != nil {
			return err
		}
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

	// ── Push tools.toml as a version reference ────────────────────────────────
	tomlRemoteKey := prefix + "/tools.toml"
	tomlRemoteURI := fmt.Sprintf("s3://%s/%s", bucket, tomlRemoteKey)
	if dryRun {
		fmt.Fprintf(w, "  %-32s  ○  would upload → %s\n", "tools.toml", tomlRemoteURI)
	} else {
		tomlArgs := []string{
			"--endpoint-url", endpoint,
			"cp", cfg.Path(), tomlRemoteURI,
		}
		if err := utils.RunExternalCLIWithEnv(
			ctx, tomlArgs, s5cmdBin, []string{"s5cmd"},
			envMap,
			nil, w, w,
		); err != nil {
			fmt.Fprintf(w, "  %-32s  ✗  upload failed: %v\n", "tools.toml", err)
		} else {
			fmt.Fprintf(w, "  %-32s  ✓  uploaded → %s\n", "tools.toml", tomlRemoteURI)
		}
	}

	// ── Build and push wave layers for wave_inject tools ─────────────────────
	// Only when at least one tool has wave_inject = true and we are not
	// filtering to a single tool (a single-tool push can't build a complete layer).
	injectTools := cfg.WaveInjectTools()
	if len(injectTools) > 0 && filterName == "" {
		fmt.Fprintf(w, "\n[abc] building wave layers for %d wave_inject tool(s)...\n", len(injectTools))

		// Collect arches present in the binary cache for the inject tools.
		archBins := map[string][]wave.ToolBin{} // arch → []ToolBin
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			for _, t := range injectTools {
				pfx := t.Name + "-linux-"
				if !strings.HasPrefix(name, pfx) {
					continue
				}
				arch := strings.TrimPrefix(name, pfx)
				if arch == "" {
					continue
				}
				archBins[arch] = append(archBins[arch], wave.ToolBin{
					Name: t.Name,
					Path: filepath.Join(binDir, name),
				})
			}
		}

		archList := make([]string, 0, len(archBins))
		for a := range archBins {
			archList = append(archList, a)
		}
		sort.Strings(archList)

		for _, arch := range archList {
			bins := archBins[arch]

			// Combined layer: wave-layer-linux-<arch>.tar.gz (all wave_inject tools).
			combinedName := "wave-layer-linux-" + arch + ".tar.gz"
			uploadWaveLayer(ctx, w, bins, combinedName, bucket, prefix, endpoint, s5cmdBin, envMap, dryRun)

			// Per-tool layers: wave-layer-linux-<arch>-<tool>.tar.gz
			// Allows --wave-inject-tools=<name> to inject only a single tool and
			// lets the Wave API receive exactly the needed layer with no extras.
			for _, b := range bins {
				perToolName := fmt.Sprintf("wave-layer-linux-%s-%s.tar.gz", arch, b.Name)
				uploadWaveLayer(ctx, w, []wave.ToolBin{b}, perToolName, bucket, prefix, endpoint, s5cmdBin, envMap, dryRun)
			}
		}
	}

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
//
// Credential resolution order per spec criterion D:
//  1. AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY from the caller's environment
//     (env-var-first; overrides everything below — CI/CD compatibility).
//  2. admin.tools.context_service names a service (default: "rustfs" if unset).
//     Read admin.services.<name>.{endpoint,access_key,secret_key} via
//     AdminFloorServiceNamed — no IsABCNodesCluster() gate.
//  3. admin.abc_nodes.s3_access_key / s3_secret_key fallback.
//  4. Exit non-zero with actionable message if all sources are empty.
//
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
		endpoint := resolveFloorEndpoint(activeCtx, svc)
		endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if endpoint == "" {
			return "", nil, false
		}
		if !isEndpointReachable(ctx, endpoint) {
			return "", nil, false
		}
		envMap := resolveS3Credentials(activeCtx, svc, endpoint)
		return endpoint, envMap, true
	}

	if ep, env, ok := try(preferred); ok {
		return ep, env, nil
	}

	// Fallback to the other service.
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

// resolveFloorEndpoint returns admin.services.<svc>.endpoint for the context.
func resolveFloorEndpoint(c config.Context, svc string) string {
	if v, ok := config.GetAdminFloorField(&c.Admin.Services, svc, "endpoint"); ok {
		return v
	}
	// Legacy fallback for minio: admin.abc_nodes.s3_endpoint (via MinioS3APIEndpoint).
	if svc == "minio" {
		return c.MinioS3APIEndpoint()
	}
	return ""
}

// resolveS3Credentials builds the AWS_* env map for s5cmd using the credential
// resolution order from spec criterion D:
//  1. AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY from caller environment.
//  2. admin.services.<svc>.access_key / secret_key via AdminFloorServiceNamed.
//  3. admin.abc_nodes.s3_access_key / s3_secret_key fallback.
func resolveS3Credentials(c config.Context, svc, endpoint string) map[string]string {
	// Step 1: caller environment wins unconditionally.
	accessKey := strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY"))

	// Step 2: admin.services.<svc> credentials (no IsABCNodesCluster() gate).
	if accessKey == "" {
		if v, ok := config.GetAdminFloorField(&c.Admin.Services, svc, "access_key"); ok {
			accessKey = v
		}
	}
	if secretKey == "" {
		if v, ok := config.GetAdminFloorField(&c.Admin.Services, svc, "secret_key"); ok {
			secretKey = v
		}
	}
	// user/password aliases for minio.
	if svc == "minio" {
		if accessKey == "" {
			if v, ok := config.GetAdminFloorField(&c.Admin.Services, svc, "user"); ok {
				accessKey = v
			}
		}
		if secretKey == "" {
			if v, ok := config.GetAdminFloorField(&c.Admin.Services, svc, "password"); ok {
				secretKey = v
			}
		}
	}

	// Step 3: admin.abc_nodes fallback.
	if n := c.ABCNodes(); n != nil {
		if accessKey == "" {
			accessKey = strings.TrimSpace(n.S3AccessKey)
		}
		if secretKey == "" {
			secretKey = strings.TrimSpace(n.S3SecretKey)
		}
		if accessKey == "" {
			accessKey = strings.TrimSpace(n.MinioRootUser)
		}
		if secretKey == "" {
			secretKey = strings.TrimSpace(n.MinioRootPassword)
		}
	}

	out := make(map[string]string)
	if accessKey != "" {
		out["AWS_ACCESS_KEY_ID"] = accessKey
	}
	if secretKey != "" {
		out["AWS_SECRET_ACCESS_KEY"] = secretKey
	}
	if endpoint != "" {
		out["AWS_ENDPOINT_URL"] = endpoint
		out["AWS_ENDPOINT_URL_S3"] = endpoint
	}
	return out
}

// ensureBucket checks whether bucket exists on the S3 endpoint and creates it
// if absent. Uses s5cmd ls to check and s5cmd mb to create. The same envMap
// used for uploads is passed so credentials are consistent. Idempotent.
func ensureBucket(ctx context.Context, w io.Writer, s5cmd, endpoint string, envMap map[string]string, bucket string) error {
	fmt.Fprintf(w, `Ensuring bucket %q on %s... `, bucket, endpoint)
	lsErr := utils.RunExternalCLIWithEnv(
		ctx,
		[]string{"--endpoint-url", endpoint, "ls", "s3://" + bucket},
		s5cmd, []string{"s5cmd"},
		envMap,
		nil, io.Discard, io.Discard,
	)
	if lsErr == nil {
		fmt.Fprintln(w, "exists")
		return nil
	}
	// Bucket absent — create it.
	if err := utils.RunExternalCLIWithEnv(
		ctx,
		[]string{"--endpoint-url", endpoint, "mb", "s3://" + bucket},
		s5cmd, []string{"s5cmd"},
		envMap,
		nil, io.Discard, w,
	); err != nil {
		return fmt.Errorf("failed to ensure bucket %q on %s: %w", bucket, endpoint, err)
	}
	fmt.Fprintln(w, "done")
	return nil
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

// uploadWaveLayer packs bins into a tar.gz wave layer and uploads it to S3.
// layerName is the basename (e.g. "wave-layer-linux-amd64.tar.gz").
func uploadWaveLayer(
	ctx context.Context, w io.Writer,
	bins []wave.ToolBin, layerName, bucket, prefix, endpoint, s5cmdBin string,
	envMap map[string]string, dryRun bool,
) {
	remoteKey := prefix + "/" + layerName
	remoteURI := fmt.Sprintf("s3://%s/%s", bucket, remoteKey)

	names := make([]string, len(bins))
	for i, b := range bins {
		names[i] = b.Name
	}

	if dryRun {
		fmt.Fprintf(w, "  %-36s  ○  would build+upload → %s [%s]\n",
			layerName, remoteURI, strings.Join(names, ", "))
		return
	}

	data, err := wave.PackToolsLayer(bins)
	if err != nil {
		fmt.Fprintf(w, "  %-36s  ✗  pack failed: %v\n", layerName, err)
		return
	}

	tmp, err := os.CreateTemp("", "wave-layer-*.tar.gz")
	if err != nil {
		fmt.Fprintf(w, "  %-36s  ✗  temp file: %v\n", layerName, err)
		return
	}
	_, writeErr := tmp.Write(data)
	tmp.Close()
	if writeErr != nil {
		os.Remove(tmp.Name())
		fmt.Fprintf(w, "  %-36s  ✗  write temp: %v\n", layerName, writeErr)
		return
	}

	uploadErr := utils.RunExternalCLIWithEnv(
		ctx,
		[]string{"--endpoint-url", endpoint, "cp", tmp.Name(), remoteURI},
		s5cmdBin, []string{"s5cmd"},
		envMap,
		nil, w, w,
	)
	os.Remove(tmp.Name())
	if uploadErr != nil {
		fmt.Fprintf(w, "  %-36s  ✗  upload failed: %v\n", layerName, uploadErr)
	} else {
		fmt.Fprintf(w, "  %-36s  ✓  uploaded → %s [%s]\n",
			layerName, remoteURI, strings.Join(names, ", "))
	}
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

