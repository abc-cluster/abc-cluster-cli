package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// GetenvFromEnviron returns the value for name in an environ slice ("KEY=value").
// If the value contains '=', everything after the first '=' is returned.
func GetenvFromEnviron(environ []string, name string) string {
	prefix := name + "="
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

// UpsertEnvOnlyMissing merges key=value from extra into base only when base
// does not already define that key with a non-empty value (after trim).
func UpsertEnvOnlyMissing(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := base
	for k, v := range extra {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		if strings.TrimSpace(GetenvFromEnviron(out, k)) != "" {
			continue
		}
		out = upsertEnv(out, map[string]string{k: v})
	}
	return out
}

// ExtractBinaryLocationFlag parses service-cli passthrough argv for delegated CLIs.
//
// Optional leading "--binary-location <path>" / "--binary-location=<path>" entries
// are consumed from the left. If the next token is "--", everything after it is the
// child argv verbatim (including further "--"). Otherwise the remainder of args
// (after any leading --binary-location entries) is the child argv as-is.
func ExtractBinaryLocationFlag(args []string) (binaryLocation string, passthrough []string, err error) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--binary-location":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--binary-location requires a value")
			}
			binaryLocation = args[i+1]
			i += 2
		case len(a) > len("--binary-location=") && a[:len("--binary-location=")] == "--binary-location=":
			binaryLocation = a[len("--binary-location="):]
			i++
		default:
			goto afterLeadingBinary
		}
	}
afterLeadingBinary:
	if i < len(args) && args[i] == "--" {
		return binaryLocation, append([]string(nil), args[i+1:]...), nil
	}
	return binaryLocation, append([]string(nil), args[i:]...), nil
}

// RunExternalCLI runs a local CLI binary with passthrough args.
//
// If binaryLocation is empty, the first available binary in binaryCandidates is
// selected from PATH.
func RunExternalCLI(ctx context.Context, args []string, binaryLocation string, binaryCandidates []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunExternalCLIWithEnv(ctx, args, binaryLocation, binaryCandidates, nil, stdin, stdout, stderr)
}

// RunExternalCLIWithEnv runs a local CLI binary with passthrough args and
// optional environment overrides (merged with os.Environ via upsertEnv).
func RunExternalCLIWithEnv(ctx context.Context, args []string, binaryLocation string, binaryCandidates []string, envOverrides map[string]string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunExternalCLIWithEnvAndBase(ctx, args, binaryLocation, binaryCandidates, os.Environ(), envOverrides, stdin, stdout, stderr)
}

// RunExternalCLIWithEnvAndBase runs a local CLI binary with cmd.Env =
// upsertEnv(baseEnviron, envOverrides). Pass base from UpsertEnvOnlyMissing
// when config-derived credentials must not override non-empty process env.
func RunExternalCLIWithEnvAndBase(ctx context.Context, args []string, binaryLocation string, binaryCandidates []string, baseEnviron []string, envOverrides map[string]string, stdin io.Reader, stdout, stderr io.Writer) error {
	binary, err := resolveCLIBinary(binaryLocation, binaryCandidates)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = upsertEnv(baseEnviron, envOverrides)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s %v: %w", binary, args, err)
	}
	return nil
}

func upsertEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	indices := make(map[string]int, len(base))
	out := make([]string, len(base))
	copy(out, base)
	for i, kv := range out {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			indices[kv[:eq]] = i
		}
	}
	for k, v := range overrides {
		if strings.TrimSpace(k) == "" || v == "" {
			continue
		}
		kv := k + "=" + v
		if idx, ok := indices[k]; ok {
			out[idx] = kv
			continue
		}
		indices[k] = len(out)
		out = append(out, kv)
	}
	return out
}

func resolveCLIBinary(binaryLocation string, binaryCandidates []string) (string, error) {
	if binaryLocation != "" {
		return binaryLocation, nil
	}
	for _, candidate := range binaryCandidates {
		if candidate == "" {
			continue
		}
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil
		}
	}
	if len(binaryCandidates) == 0 {
		return "", fmt.Errorf("no CLI binary candidates configured")
	}
	return "", fmt.Errorf("none of the CLI binaries were found in PATH: %v (or set --binary-location)", binaryCandidates)
}
