package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
