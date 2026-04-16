package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ExtractBinaryLocationFlag parses passthrough CLI args and extracts
// --binary-location while preserving all other args for the delegated CLI.
//
// Use `--` to stop parsing and pass all remaining args through verbatim.
func ExtractBinaryLocationFlag(args []string) (binaryLocation string, passthrough []string, err error) {
	passthrough = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			passthrough = append(passthrough, args[i+1:]...)
			return binaryLocation, passthrough, nil
		case a == "--binary-location":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--binary-location requires a value")
			}
			binaryLocation = args[i+1]
			i++
		case len(a) > len("--binary-location=") && a[:len("--binary-location=")] == "--binary-location=":
			binaryLocation = a[len("--binary-location="):]
		default:
			passthrough = append(passthrough, a)
		}
	}

	return binaryLocation, passthrough, nil
}

// RunExternalCLI runs a local CLI binary with passthrough args.
//
// If binaryLocation is empty, the first available binary in binaryCandidates is
// selected from PATH.
func RunExternalCLI(ctx context.Context, args []string, binaryLocation string, binaryCandidates []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return RunExternalCLIWithEnv(ctx, args, binaryLocation, binaryCandidates, nil, stdin, stdout, stderr)
}

// RunExternalCLIWithEnv runs a local CLI binary with passthrough args and
// optional environment overrides.
func RunExternalCLIWithEnv(ctx context.Context, args []string, binaryLocation string, binaryCandidates []string, envOverrides map[string]string, stdin io.Reader, stdout, stderr io.Writer) error {
	binary, err := resolveCLIBinary(binaryLocation, binaryCandidates)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = upsertEnv(os.Environ(), envOverrides)

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
