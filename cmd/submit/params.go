package submit

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// buildParamsFile creates a temporary YAML params file from the provided
// inputs. Returns the file path and a cleanup function. If input, output,
// and extra are all empty the returned path is "" and cleanup is a no-op.
func buildParamsFile(input, output string, extra []string) (path string, cleanup func(), err error) {
	noop := func() {}

	params := map[string]any{}
	if input != "" {
		params["input"] = input
	}
	if output != "" {
		params["outdir"] = output
	}
	for _, kv := range extra {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return "", noop, fmt.Errorf("--param %q must be in key=value format", kv)
		}
		params[k] = v
	}

	if len(params) == 0 {
		return "", noop, nil
	}

	data, err := yaml.Marshal(params)
	if err != nil {
		return "", noop, fmt.Errorf("marshalling params: %w", err)
	}

	f, err := os.CreateTemp("", "abc-submit-params-*.yaml")
	if err != nil {
		return "", noop, fmt.Errorf("creating temp params file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", noop, fmt.Errorf("writing params file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", noop, fmt.Errorf("closing params file: %w", err)
	}

	path = f.Name()
	cleanup = func() { os.Remove(path) }
	return path, cleanup, nil
}
