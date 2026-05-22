package job

import (
	"fmt"
	"strings"

	jobhcl "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/job"
)

// Supported software-stack runtime identifiers (orthogonal to Nomad --driver).
const (
	runtimePixiExec   = "pixi-exec"
	runtimeMicromamba = "micromamba-exec"
	runtimeWaveExec   = "wave-exec"
)

// NormalizeRuntimeID returns a canonical runtime token or "" if s is empty.
// Accepted aliases:
//
//	"pixi"                → pixi-exec
//	"micromamba", "mamba" → micromamba-exec
//	"wave"                → wave-exec
func NormalizeRuntimeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "pixi":
		return runtimePixiExec
	case "micromamba", "mamba":
		return runtimeMicromamba
	case "wave":
		return runtimeWaveExec
	}
	return s
}

// ValidateRuntimeDriver checks --runtime/--from-file and runtime×driver compatibility
// after the task driver has been normalized.
func ValidateRuntimeDriver(spec *jobSpec) error {
	if spec == nil {
		return nil
	}
	rt := NormalizeRuntimeID(spec.Runtime)
	spec.Runtime = rt

	from := strings.TrimSpace(spec.From)
	if rt == "" {
		if from != "" {
			return fmt.Errorf("--from-file requires --runtime (e.g. pixi-exec, micromamba-exec)")
		}
		return nil
	}

	switch rt {
	case runtimePixiExec:
		if from == "" {
			return fmt.Errorf("runtime %q requires --from-file=<pixi.toml or pixi.lock>", rt)
		}
		if strings.ContainsAny(from, "\r\n\x00") {
			return fmt.Errorf("runtime %q: --from-file must be a single-line path (no newline or NUL characters)", rt)
		}
		fromLower := strings.ToLower(from)
		if !strings.HasSuffix(fromLower, ".toml") && !strings.HasSuffix(fromLower, ".lock") {
			return fmt.Errorf("runtime %q: --from-file must end with .toml (pixi.toml) or .lock (pixi.lock)", rt)
		}
		if spec.NoNetwork {
			return fmt.Errorf("runtime %q needs network access to solve and download packages; remove --no-network or omit --runtime", rt)
		}
		if strings.TrimSpace(spec.Conda) != "" {
			return fmt.Errorf("runtime %q cannot be combined with conda (--conda / #ABC --conda); use only one stack", rt)
		}

	case runtimeMicromamba:
		if from == "" {
			return fmt.Errorf("runtime %q requires --from-file=<path-to-environment.yml>", rt)
		}
		if strings.ContainsAny(from, "\r\n\x00") {
			return fmt.Errorf("runtime %q: --from-file must be a single-line path (no newline or NUL characters)", rt)
		}
		fromLower := strings.ToLower(from)
		if !strings.HasSuffix(fromLower, ".yml") && !strings.HasSuffix(fromLower, ".yaml") {
			return fmt.Errorf("runtime %q: --from-file must end with .yml or .yaml (path to conda environment file)", rt)
		}
		if spec.NoNetwork {
			return fmt.Errorf("runtime %q needs network access to download packages; remove --no-network or omit --runtime", rt)
		}
		if strings.TrimSpace(spec.Conda) != "" {
			return fmt.Errorf("runtime %q cannot be combined with --conda; use only one stack", rt)
		}

	case runtimeWaveExec:
		if from == "" {
			return fmt.Errorf("runtime %q requires --from-file=<path-to-environment.yml>", rt)
		}
		if strings.ContainsAny(from, "\r\n\x00") {
			return fmt.Errorf("runtime %q: --from-file must be a single-line path (no newline or NUL characters)", rt)
		}
		fromLower := strings.ToLower(from)
		if !strings.HasSuffix(fromLower, ".yml") && !strings.HasSuffix(fromLower, ".yaml") {
			return fmt.Errorf("runtime %q: --from-file must end with .yml or .yaml (path to conda environment file)", rt)
		}
		if spec.NoNetwork {
			return fmt.Errorf("runtime %q needs network access to build and pull the Wave container; remove --no-network", rt)
		}
		if strings.TrimSpace(spec.Conda) != "" {
			return fmt.Errorf("runtime %q cannot be combined with --conda; use only one stack", rt)
		}
		if spec.Pixi {
			return fmt.Errorf("runtime %q cannot be combined with --pixi; use only one stack", rt)
		}

	default:
		return fmt.Errorf("unsupported runtime %q (supported: pixi-exec (alias pixi), micromamba-exec (aliases: micromamba, mamba), wave-exec (alias wave))", spec.Runtime)
	}

	return validateDriverForRuntime(strings.TrimSpace(spec.Driver), rt)
}

func validateDriverForRuntime(driver, runtime string) error {
	if driver == "" {
		return fmt.Errorf("internal: task driver is empty before runtime validation")
	}
	switch runtime {
	case runtimePixiExec, runtimeMicromamba:
		if driver == "slurm" {
			return fmt.Errorf(`runtime %q is not supported with task driver "slurm" (use "exec", "raw_exec", "docker", "containerd-driver", or "hpc-bridge")`, runtime)
		}
		switch driver {
		case "exec", "raw_exec", "docker", "containerd-driver", "hpc-bridge":
			return nil
		default:
			return fmt.Errorf("runtime %q is not supported with task driver %q (allowed: exec, raw_exec, docker, containerd-driver, hpc-bridge)",
				runtime, driver)
		}
	case runtimeWaveExec:
		// Wave builds a container image; the main task must use a container driver.
		switch driver {
		case "docker", "containerd-driver":
			return nil
		default:
			return fmt.Errorf("runtime %q requires a container task driver (docker or containerd-driver), got %q", runtime, driver)
		}
	default:
		return nil
	}
}

// FinalizeJobScript validates runtime/driver settings and rewrites the script
// body when a runtime wrapper is required.
func FinalizeJobScript(spec *jobSpec, scriptName, script string) (string, error) {
	if err := ValidateRuntimeDriver(spec); err != nil {
		return "", err
	}
	var prefix []string

	// --sleep: inject a debug sleep as the very first action after the shebang
	// so operators can `abc job exec` / `nomad alloc exec` into the running
	// allocation before the workload begins.
	if spec != nil && spec.DebugSleepSecs > 0 {
		prefix = append(prefix,
			fmt.Sprintf("# DEBUG: sleeping %ds to allow interactive exec (--sleep)", spec.DebugSleepSecs),
			fmt.Sprintf("echo '[abc] debug sleep: %ds — exec into this alloc now if needed'", spec.DebugSleepSecs),
			fmt.Sprintf("sleep %d", spec.DebugSleepSecs),
			"echo '[abc] debug sleep done — starting workload'",
		)
	}

	prefix = append(prefix, taskTmpPreambleLines(spec)...)
	rt := ""
	if spec != nil {
		rt = NormalizeRuntimeID(spec.Runtime)
	}
	switch rt {
	case "":
	case runtimePixiExec:
		binaryURL := ""
		if spec != nil {
			binaryURL = spec.PixiBinaryURL
		}
		w, err := pixiWrapperLines(scriptName, strings.TrimSpace(spec.From), spec.Driver, spec.PixiCleanup, binaryURL, spec.PixiInstallLocked)
		if err != nil {
			return "", err
		}
		prefix = append(prefix, w...)
	case runtimeMicromamba:
		binaryURL := ""
		if spec != nil {
			binaryURL = spec.MicromambaBinaryURL
		}
		w, err := micromambaWrapperLines(scriptName, strings.TrimSpace(spec.From), spec.Driver, spec.MicromambaCleanup, binaryURL)
		if err != nil {
			return "", err
		}
		prefix = append(prefix, w...)
	case runtimeWaveExec:
		// wave-exec: the container is built by the Wave prestart task; the main task
		// runs inside the pre-built Docker image with no script wrapper needed.
	default:
		return "", fmt.Errorf("internal: missing script wrapper for runtime %q", rt)
	}
	if len(prefix) == 0 {
		return script, nil
	}
	return insertLinesAfterShebang(script, prefix), nil
}

// insertLinesAfterShebang splices insert after the first line when it is a shebang.
func insertLinesAfterShebang(script string, insert []string) string {
	if len(insert) == 0 {
		return script
	}
	lines := strings.Split(script, "\n")
	insertAt := 0
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "#!") {
		insertAt = 1
	}
	out := strings.Join(lines[:insertAt], "\n")
	if out != "" {
		out += "\n"
	}
	out += strings.Join(insert, "\n")
	if insertAt < len(lines) {
		rest := strings.Join(lines[insertAt:], "\n")
		if rest != "" {
			out += "\n" + rest
		}
	}
	return out
}

// pixiWrapperLines builds the guard block that re-execs the script inside pixi.
//
// Local mode (manifestPath starts with "${NOMAD_TASK_DIR}"): the pixi.toml is
// staged as a Nomad template. The wrapper downloads the pixi binary via curl
// (using shell-side uname detection), sets PIXI_HOME to a job-local directory,
// runs `pixi install` (optionally with --locked when locked is true), then
// re-execs under `pixi run`. An optional cleanup trap removes the env dir.
//
// Legacy remote mode: pixi is assumed pre-installed on the execution host.
//
// Note: Pixi's `exec` subcommand does not support --manifest-path (as of
// pixi 0.46); workspace-bound execution uses `pixi run --manifest-path`.
func pixiWrapperLines(scriptName, manifestPath, driver string, cleanup bool, binaryURL string, locked bool) ([]string, error) {
	if scriptName == "" {
		return nil, fmt.Errorf("internal: empty script name for pixi wrapper")
	}
	innerArg := jobhcl.ScriptArgForDriver(driver, scriptName)
	innerQuoted := bashDoubleQuote(innerArg)

	// Local mode: pixi.toml is staged as a Nomad template; binary is downloaded
	// via curl in the wrapper using shell uname for platform detection.
	if strings.HasPrefix(manifestPath, "${NOMAD_TASK_DIR}") {
		qManifest := bashDoubleQuote(manifestPath)
		lines := []string{
			`if [ "${ABC_RUNTIME_PIXI_PHASE:-}" != inner ]; then`,
			`  export ABC_RUNTIME_PIXI_PHASE=inner`,
		}
		if binaryURL != "" {
			qURL := bashDoubleQuote(binaryURL)
			lines = append(lines,
				`  _ABC_KERNEL="$(uname -s | tr '[:upper:]' '[:lower:]')"`,
				`  _ABC_ARCH="$(uname -m)"`,
				`  case "${_ABC_ARCH}" in x86_64) _ABC_ARCH=amd64 ;; aarch64) _ABC_ARCH=arm64 ;; esac`,
				fmt.Sprintf(`  curl -fsSL %s-"${_ABC_KERNEL}-${_ABC_ARCH}" -o "${NOMAD_TASK_DIR}/pixi" || { echo "[abc] pixi download failed" >&2; exit 1; }`, qURL),
			)
		}
		lines = append(lines,
			`  chmod +x "${NOMAD_TASK_DIR}/pixi"`,
			`  export PATH="${NOMAD_TASK_DIR}:${PATH}"`,
			`  export PIXI_HOME="${NOMAD_TASK_DIR}/.pixi"`,
		)
		if cleanup {
			lines = append(lines,
				`  trap 'rm -rf "${NOMAD_TASK_DIR}/.pixi"' EXIT`,
			)
		}
		installCmd := fmt.Sprintf(`  pixi install --manifest-path %s`, qManifest)
		if locked {
			installCmd += " --locked"
		}
		lines = append(lines,
			installCmd,
			fmt.Sprintf(`  exec pixi run --manifest-path %s -- /bin/bash %s "$@"`, qManifest, innerQuoted),
			`fi`,
			``,
		)
		return lines, nil
	}

	// Legacy remote mode: pixi is pre-installed on the execution host.
	// Inner invocation must match ScriptArgForDriver so docker/containerd do not
	// resolve .../local/... twice (see internal/hclgen/job ociTaskScriptArg).
	qManifest := shellSingleQuote(manifestPath)
	return []string{
		`if [ "${ABC_RUNTIME_PIXI_PHASE:-}" != inner ]; then`,
		`  export ABC_RUNTIME_PIXI_PHASE=inner`,
		fmt.Sprintf(`  exec pixi run --manifest-path %s -- /bin/bash %s "$@"`, qManifest, innerQuoted),
		`fi`,
		``,
	}, nil
}

func prependPixiWorkspaceWrapper(script, scriptName, manifestPath, driver string) (string, error) {
	wrapper, err := pixiWrapperLines(scriptName, manifestPath, driver, false, "", false)
	if err != nil {
		return "", err
	}
	return insertLinesAfterShebang(script, wrapper), nil
}

// micromambaWrapperLines builds the guard block that creates a conda environment
// via micromamba and re-execs the script inside it.
//
// Local mode (envPath starts with "${NOMAD_TASK_DIR}"): the environment.yml is
// staged as a Nomad template. The wrapper downloads micromamba via curl, creates
// the conda env at ${NOMAD_TASK_DIR}/.mamba/env, then re-execs via `micromamba run`.
// An optional cleanup trap removes the env dir on exit.
//
// Legacy remote mode: micromamba is assumed pre-installed on the execution host.
func micromambaWrapperLines(scriptName, envPath, driver string, cleanup bool, binaryURL string) ([]string, error) {
	if scriptName == "" {
		return nil, fmt.Errorf("internal: empty script name for micromamba wrapper")
	}
	innerArg := jobhcl.ScriptArgForDriver(driver, scriptName)
	innerQuoted := bashDoubleQuote(innerArg)
	envPrefix := `"${NOMAD_TASK_DIR}/.mamba/env"`

	// Local mode: environment.yml staged as a Nomad template; binary downloaded via curl.
	if strings.HasPrefix(envPath, "${NOMAD_TASK_DIR}") {
		qEnvFile := bashDoubleQuote(envPath)
		lines := []string{
			`if [ "${ABC_RUNTIME_MAMBA_PHASE:-}" != inner ]; then`,
			`  export ABC_RUNTIME_MAMBA_PHASE=inner`,
		}
		if binaryURL != "" {
			qURL := bashDoubleQuote(binaryURL)
			lines = append(lines,
				`  _ABC_KERNEL="$(uname -s | tr '[:upper:]' '[:lower:]')"`,
				`  _ABC_ARCH="$(uname -m)"`,
				`  case "${_ABC_ARCH}" in x86_64) _ABC_ARCH=amd64 ;; aarch64) _ABC_ARCH=arm64 ;; esac`,
				fmt.Sprintf(`  curl -fsSL %s-"${_ABC_KERNEL}-${_ABC_ARCH}" -o "${NOMAD_TASK_DIR}/micromamba" || { echo "[abc] micromamba download failed" >&2; exit 1; }`, qURL),
			)
		}
		lines = append(lines,
			`  chmod +x "${NOMAD_TASK_DIR}/micromamba"`,
			`  export MAMBA_ROOT_PREFIX="${NOMAD_TASK_DIR}/.mamba"`,
		)
		if cleanup {
			lines = append(lines,
				`  trap 'rm -rf "${NOMAD_TASK_DIR}/.mamba"' EXIT`,
			)
		}
		lines = append(lines,
			fmt.Sprintf(`  "${NOMAD_TASK_DIR}/micromamba" env create --file %s --prefix %s --yes --quiet || { echo "[abc] micromamba env create failed" >&2; exit 1; }`, qEnvFile, envPrefix),
			fmt.Sprintf(`  exec "${NOMAD_TASK_DIR}/micromamba" run --prefix %s -- /bin/bash %s "$@"`, envPrefix, innerQuoted),
			`fi`,
			``,
		)
		return lines, nil
	}

	// Legacy remote mode: micromamba is pre-installed on the execution host.
	qEnvFile := shellSingleQuote(envPath)
	return []string{
		`if [ "${ABC_RUNTIME_MAMBA_PHASE:-}" != inner ]; then`,
		`  export ABC_RUNTIME_MAMBA_PHASE=inner`,
		`  export MAMBA_ROOT_PREFIX="${NOMAD_TASK_DIR}/.mamba"`,
		fmt.Sprintf(`  micromamba env create --file %s --prefix %s --yes --quiet || { echo "[abc] micromamba env create failed" >&2; exit 1; }`, qEnvFile, envPrefix),
		fmt.Sprintf(`  exec micromamba run --prefix %s -- /bin/bash %s "$@"`, envPrefix, innerQuoted),
		`fi`,
		``,
	}, nil
}

func shellSingleQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'"'"'`) + `'`
}

func bashDoubleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// syncStackMetaKeys writes abc_runtime / abc_from_file from spec fields into
// meta. Call after any source (preamble, CLI, params) may have set Runtime
// or From.
//
// Meta-key naming aligns with the CLI flag rename (`--from` → `--from-file`,
// 2026-05-22). The legacy `abc_from` key is intentionally NOT emitted —
// clean break, no deprecation alias.
func syncStackMetaKeys(spec *jobSpec) {
	if spec == nil {
		return
	}
	rt := NormalizeRuntimeID(spec.Runtime)
	from := strings.TrimSpace(spec.From)
	if rt == "" && from == "" {
		return
	}
	if spec.Meta == nil {
		spec.Meta = map[string]string{}
	}
	if rt != "" {
		spec.Meta["abc_runtime"] = rt
	}
	if from != "" {
		spec.Meta["abc_from_file"] = from
	}
}
