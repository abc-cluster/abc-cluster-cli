package job

import (
	"fmt"
	"strings"

	jobhcl "github.com/abc-cluster/abc-cluster-cli/internal/hclgen/job"
)

// Supported software-stack runtime identifiers (orthogonal to Nomad --driver).
const (
	runtimePixiExec = "pixi-exec"
)

// NormalizeRuntimeID returns a canonical runtime token or "" if s is empty.
// "pixi" is accepted as an alias for pixi-exec.
func NormalizeRuntimeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "pixi" {
		return runtimePixiExec
	}
	return s
}

// ValidateRuntimeDriver checks --runtime/--from and runtime×driver compatibility
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
			return fmt.Errorf("--from requires --runtime (e.g. pixi-exec)")
		}
		return nil
	}

	switch rt {
	case runtimePixiExec:
		if from == "" {
			return fmt.Errorf("runtime %q requires --from=<path-to-pixi.toml>", rt)
		}
		if strings.ContainsAny(from, "\r\n\x00") {
			return fmt.Errorf("runtime %q: --from must be a single-line path (no newline or NUL characters)", rt)
		}
		if !strings.HasSuffix(strings.ToLower(from), ".toml") {
			return fmt.Errorf("runtime %q: --from must end with .toml (path to pixi.toml on the execution host)", rt)
		}
		if spec.NoNetwork {
			return fmt.Errorf("runtime %q needs network access to solve and download packages; remove --no-network or omit --runtime", rt)
		}
		if strings.TrimSpace(spec.Conda) != "" {
			return fmt.Errorf("runtime %q cannot be combined with conda (--conda / #ABC --conda); use only one stack", rt)
		}
	default:
		return fmt.Errorf("unsupported runtime %q (supported: pixi-exec, alias pixi)", spec.Runtime)
	}

	return validateDriverForRuntime(strings.TrimSpace(spec.Driver), rt)
}

func validateDriverForRuntime(driver, runtime string) error {
	if driver == "" {
		return fmt.Errorf("internal: task driver is empty before runtime validation")
	}
	switch runtime {
	case runtimePixiExec:
		if driver == "slurm" {
			return fmt.Errorf(`runtime %q is not supported with task driver "slurm" (the batch script is passed inline to the bridge and is not materialized as a task-local file; use "exec", "docker", "containerd-driver", or "hpc-bridge")`, runtime)
		}
		switch driver {
		case "exec", "raw_exec", "docker", "containerd-driver", "hpc-bridge":
			return nil
		default:
			return fmt.Errorf("runtime %q is not supported with task driver %q (allowed drivers: exec, raw_exec, docker, containerd-driver, hpc-bridge)",
				runtime, driver)
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
	out, err := applyRuntimeScriptWrap(spec, scriptName, script)
	if err != nil {
		return "", err
	}
	return out, nil
}

func applyRuntimeScriptWrap(spec *jobSpec, scriptName, script string) (string, error) {
	rt := NormalizeRuntimeID(spec.Runtime)
	if rt == "" {
		return script, nil
	}
	switch rt {
	case runtimePixiExec:
		return prependPixiWorkspaceWrapper(script, scriptName, strings.TrimSpace(spec.From), spec.Driver)
	default:
		return "", fmt.Errorf("internal: missing script wrapper for runtime %q", rt)
	}
}

// prependPixiWorkspaceWrapper injects a guard that re-execs the script under
// `pixi run --manifest-path <manifest>`, so the user's preamble and body run
// inside the Pixi default environment.
//
// Note: Pixi's `exec` subcommand does not support --manifest-path (as of
// pixi 0.46); workspace-bound execution uses `pixi run --manifest-path`.
func prependPixiWorkspaceWrapper(script, scriptName, manifestPath, driver string) (string, error) {
	if scriptName == "" {
		return "", fmt.Errorf("internal: empty script name for pixi wrapper")
	}
	lines := strings.Split(script, "\n")
	insertAt := 0
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "#!") {
		insertAt = 1
	}
	qManifest := shellSingleQuote(manifestPath)
	innerArg := jobhcl.ScriptArgForDriver(driver, scriptName)
	innerQuoted := bashDoubleQuote(innerArg)
	// Inner invocation must match ScriptArgForDriver so docker/containerd do not
	// resolve .../local/... twice (see internal/hclgen/job ociTaskScriptArg).
	wrapper := []string{
		`if [ "${ABC_RUNTIME_PIXI_PHASE:-}" != inner ]; then`,
		`  export ABC_RUNTIME_PIXI_PHASE=inner`,
		fmt.Sprintf(`  exec pixi run --manifest-path %s -- /bin/bash %s "$@"`, qManifest, innerQuoted),
		`fi`,
		``,
	}
	out := strings.Join(lines[:insertAt], "\n")
	if out != "" {
		out += "\n"
	}
	out += strings.Join(wrapper, "\n")
	out += strings.Join(lines[insertAt:], "\n")
	return out, nil
}

func shellSingleQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'"'"'`) + `'`
}

func bashDoubleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// syncStackMetaKeys writes abc_runtime / abc_from from spec fields into meta.
// Call after any source (preamble, CLI, params) may have set Runtime or From.
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
		spec.Meta["abc_from"] = from
	}
}
