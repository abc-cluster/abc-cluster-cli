package job

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/tools"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/wave"
)

const (
	waveDefaultTokenSecret = "nomad/jobs:wave_token"
	waveDefaultPlatform    = "linux/amd64"
)

// resolveWaveLocalMode handles --runtime=wave-exec by:
//  1. Reading the environment.yml and embedding its content.
//  2. Calling the Wave CLI (no --await) to resolve the deterministic target
//     image URI instantly — the image is known before the build finishes.
//  3. Injecting the target image into DriverConfig["image"] so the main task
//     pulls the Wave-built container.
//  4. Fetching the wave binary URL for the prestart artifact stanza.
//
// The companion prestart task (emitted by the HCL generator) calls wave with
// --await to block until the image is actually pullable before the main task
// starts.
func resolveWaveLocalMode(spec *jobSpec) error {
	if NormalizeRuntimeID(spec.Runtime) != runtimeWaveExec {
		return nil
	}
	from := strings.TrimSpace(spec.From)
	if from == "" || strings.Contains(from, "${") {
		return nil // remote/already-resolved path — skip local resolution
	}

	content, err := os.ReadFile(from)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("wave-exec: --from=%q: file not found", from)
		}
		return fmt.Errorf("wave-exec: cannot read %q: %w", from, err)
	}
	spec.WaveEnvContent = string(content)

	// Resolve the wave binary URL for the Nomad artifact stanza.
	waveURL, err := resolveToolBinaryURL("wave")
	if err != nil {
		return fmt.Errorf("wave-exec: %w", err)
	}
	spec.WaveBinaryURL = waveURL

	// Determine the target platform.
	platform := spec.WavePlatform
	if platform == "" {
		platform = waveDefaultPlatform
	}

	// Call wave CLI without --await to get the targetImage URI immediately.
	// Wave computes the URI deterministically from the input hash, so the URI
	// is known before the build completes.
	targetImage, err := invokeWaveCLI(from, platform)
	if err != nil {
		return fmt.Errorf("wave-exec: %w", err)
	}
	spec.WaveTargetImage = targetImage

	// Inject the Wave image as the docker driver image.
	if spec.DriverConfig == nil {
		spec.DriverConfig = make(map[string]string)
	}
	spec.DriverConfig["image"] = targetImage

	// Rewrite From to the staged path inside the Nomad task dir.
	spec.From = "${NOMAD_TASK_DIR}/environment.yml"
	syncStackMetaKeys(spec)
	return nil
}

// waveResponse is the JSON object returned by `wave -o json`.
type waveResponse struct {
	TargetImage string `json:"targetImage"`
	BuildID     string `json:"buildId"`
}

// invokeWaveCLI calls the wave binary with --conda-file <condaFile> --platform
// <platform> -o json (no --await) and returns the targetImage URI.
func invokeWaveCLI(condaFile, platform string) (string, error) {
	waveBin, err := exec.LookPath("wave")
	if err != nil {
		return "", fmt.Errorf(
			"wave CLI not found in PATH\n"+
				"  Install from: https://github.com/seqeralabs/wave-cli/releases\n"+
				"  Required for: --runtime=wave-exec",
		)
	}
	args := []string{
		"--conda-file", condaFile,
		"--platform", platform,
		"-o", "json",
	}
	cmd := exec.Command(waveBin, args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("wave exited %d: %s",
				exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("wave: %w", err)
	}
	var resp waveResponse
	if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
		return "", fmt.Errorf("wave: unexpected output %q: %w", strings.TrimSpace(string(out)), jsonErr)
	}
	if resp.TargetImage == "" {
		return "", fmt.Errorf("wave: empty targetImage in response: %s", string(out))
	}
	return resp.TargetImage, nil
}

// resolveWaveInjectMode handles #ABC --wave-inject-tools by:
//  1. Reading tools.toml to find the matching wave_inject tools.
//  2. Downloading the pre-built wave-layer-linux-<arch>.tar.gz from the cluster
//     S3 endpoint and extracting the requested binaries to a temp staging dir.
//  3. Calling the Wave CLI with --layer <staging-dir> to augment the base image.
//  4. Replacing DriverConfig["image"] with the resolved Wave target image.
//
// No prestart task is emitted — the image swap happens at submit time and the
// augmented image is pulled by the main task like any other Docker image.
func resolveWaveInjectMode(spec *jobSpec) error {
	if len(spec.WaveInjectTools) == 0 {
		return nil
	}
	if NormalizeRuntimeID(spec.Runtime) == runtimeWaveExec {
		return fmt.Errorf("--wave-inject-tools cannot be combined with --runtime=wave-exec")
	}
	baseImage := ""
	if spec.DriverConfig != nil {
		baseImage = spec.DriverConfig["image"]
	}
	if baseImage == "" {
		return fmt.Errorf("--wave-inject-tools requires a base image: set --driver.config.image=<image>")
	}

	// Determine target arch from WavePlatform (default linux/amd64).
	platform := spec.WavePlatform
	if platform == "" {
		platform = waveDefaultPlatform
	}
	parts := strings.SplitN(platform, "/", 2)
	if len(parts) != 2 || parts[0] != "linux" {
		return fmt.Errorf("--wave-inject-tools: unsupported platform %q (expected linux/<arch>)", platform)
	}
	arch := parts[1]

	// Load config once — used for the tools endpoint and abc-wave URL.
	cfg, err := abccfg.Load()
	if err != nil {
		return fmt.Errorf("wave-inject-tools: load config: %w", err)
	}
	assetDir, err := utils.AssetDir()
	if err != nil {
		return fmt.Errorf("wave-inject-tools: asset dir: %w", err)
	}
	toolsTomlPath := filepath.Join(assetDir, "tools.toml")
	tcfg, err := tools.ReadToolsConfig(toolsTomlPath)
	if err != nil {
		return fmt.Errorf("wave-inject-tools: read tools.toml: %w", err)
	}

	// Determine which tools to inject.
	wantAll := len(spec.WaveInjectTools) == 1 && spec.WaveInjectTools[0] == "*"
	wantSet := map[string]bool{}
	for _, n := range spec.WaveInjectTools {
		wantSet[n] = true
	}

	var injectTools []tools.ToolSpec
	for _, t := range tcfg.WaveInjectTools() {
		if wantAll || wantSet[t.Name] {
			injectTools = append(injectTools, t)
		}
	}
	if len(injectTools) == 0 {
		return fmt.Errorf("wave-inject-tools: no matching wave_inject tools found in tools.toml for %v", spec.WaveInjectTools)
	}

	// Resolve S3 endpoint and abc-wave URL from config.
	ep := strings.TrimRight(cfg.ActiveCtx().ToolPushEndpoint(), "/")
	if ep == "" {
		return fmt.Errorf(
			"wave-inject-tools: no tools endpoint configured\n" +
				"  Run once: abc admin tools push   (writes endpoint to config)\n" +
				"  Or set:   abc config set contexts.<name>.admin.tools.endpoint http://<host>:<port>",
		)
	}
	actx := cfg.ActiveCtx()
	waveAPIURL, _ := abccfg.GetAdminFloorField(&actx.Admin.Services, "wave", "http")
	if waveAPIURL == "" {
		return fmt.Errorf(
			"wave-inject-tools: no abc-wave URL configured\n" +
				"  Set: abc config set contexts.<name>.admin.services.wave.http http://<host>:<port>",
		)
	}

	// Build the list of layer inputs.
	//   *  → one combined layer  (wave-layer-linux-<arch>.tar.gz)
	//   subset → one per-tool layer per requested tool; Wave accepts multiple layers
	//            in a single /v1alpha2/container call.
	var layerInputs []waveLayerInput
	if wantAll {
		url := fmt.Sprintf("%s/%s/%s/wave-layer-linux-%s.tar.gz",
			ep, tcfg.Push.Bucket, tcfg.Push.Prefix, arch)
		d, err := fetchLayerDigests(url)
		if err != nil {
			return fmt.Errorf("wave-inject-tools: %w", err)
		}
		layerInputs = []waveLayerInput{{URL: url, Digests: d}}
	} else {
		for _, t := range injectTools {
			url := fmt.Sprintf("%s/%s/%s/wave-layer-linux-%s-%s.tar.gz",
				ep, tcfg.Push.Bucket, tcfg.Push.Prefix, arch, t.Name)
			d, err := fetchLayerDigests(url)
			if err != nil {
				return fmt.Errorf("wave-inject-tools: tool %s: %w", t.Name, err)
			}
			layerInputs = append(layerInputs, waveLayerInput{URL: url, Digests: d})
		}
	}

	// Call Wave REST API — passes all layer URLs; Wave fetches them server-side.
	targetImage, err := callWaveAPI(context.Background(), waveAPIURL, baseImage, layerInputs)
	if err != nil {
		return fmt.Errorf("wave-inject-tools: %w", err)
	}

	if spec.DriverConfig == nil {
		spec.DriverConfig = make(map[string]string)
	}
	spec.DriverConfig["image"] = targetImage
	return nil
}

// waveContainerLayer is a single layer entry in the Wave /v1alpha2/container request.
type waveContainerLayer struct {
	Location   string `json:"location"`
	GzipDigest string `json:"gzipDigest"`
	GzipSize   int64  `json:"gzipSize"`
	TarDigest  string `json:"tarDigest"`
}

// waveContainerConfig is the containerConfig field of the Wave request.
type waveContainerConfig struct {
	Layers []waveContainerLayer `json:"layers"`
}

// waveContainerRequest is the body for POST /v1alpha2/container.
type waveContainerRequest struct {
	ContainerImage  string              `json:"containerImage"`
	ContainerConfig waveContainerConfig `json:"containerConfig"`
}

// waveLayerInput is a layer URL + pre-computed digests for the Wave API.
type waveLayerInput struct {
	URL     string
	Digests wave.LayerDigests
}

// fetchLayerDigests downloads the layer at url and returns its sha256 digests.
// The download is needed to compute the gzip and tar digests required by the Wave API.
func fetchLayerDigests(url string) (wave.LayerDigests, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return wave.LayerDigests{}, fmt.Errorf("download layer from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return wave.LayerDigests{}, fmt.Errorf("download layer %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return wave.LayerDigests{}, fmt.Errorf("read layer from %s: %w", url, err)
	}
	return wave.ComputeLayerDigests(data)
}

// callWaveAPI calls the Wave REST API to augment baseImage with one or more
// pre-built layers. Wave fetches each layer from its URL server-side, so there
// is no local size constraint. Returns the resolved targetImage URI.
func callWaveAPI(ctx context.Context, waveURL, baseImage string, layers []waveLayerInput) (string, error) {
	wl := make([]waveContainerLayer, len(layers))
	for i, l := range layers {
		wl[i] = waveContainerLayer{
			Location:   l.URL,
			GzipDigest: l.Digests.GzipDigest,
			GzipSize:   l.Digests.GzipSize,
			TarDigest:  l.Digests.TarDigest,
		}
	}
	reqBody := waveContainerRequest{
		ContainerImage:  baseImage,
		ContainerConfig: waveContainerConfig{Layers: wl},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("wave API: marshal request: %w", err)
	}

	apiURL := strings.TrimRight(waveURL, "/") + "/v1alpha2/container"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("wave API: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("wave API: POST %s: %w", apiURL, err)
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return "", fmt.Errorf("wave API: POST %s: HTTP %d: %s", apiURL, httpResp.StatusCode, strings.TrimSpace(string(body)))
	}

	var resp waveResponse
	if jsonErr := json.Unmarshal(body, &resp); jsonErr != nil {
		return "", fmt.Errorf("wave API: unexpected response %q: %w", strings.TrimSpace(string(body)), jsonErr)
	}
	if resp.TargetImage == "" {
		return "", fmt.Errorf("wave API: empty targetImage in response: %s", string(body))
	}
	return resp.TargetImage, nil
}

// parseWaveTokenSecret splits a "nomad/path:key" string into path and key.
// Falls back to waveDefaultTokenSecret when s is empty.
func parseWaveTokenSecret(s string) (path, key string) {
	if s == "" {
		s = waveDefaultTokenSecret
	}
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return s, "wave_token"
}
