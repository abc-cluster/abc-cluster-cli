package job

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

// loadParamsFile reads a YAML params file and converts it to a slice of
// "--key=value" directive strings compatible with applyDirective. Nested keys
// are dot-flattened: foo.bar=baz → --foo.bar=baz.
func loadParamsFile(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := utils.LoadParamsFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for k, v := range raw {
		if err := flattenParams(k, v, &out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// flattenParams recursively flattens a nested map/slice value into
// "--prefix=value" directive strings.
func flattenParams(prefix string, value any, out *[]string) error {
	switch v := value.(type) {
	case map[string]any:
		if prefix == "meta" {
			for k, x := range v {
				*out = append(*out, fmt.Sprintf("--meta=%s=%v", k, x))
			}
			return nil
		}
		if prefix == "driver.config" {
			for k, x := range v {
				*out = append(*out, fmt.Sprintf("--driver.config.%s=%v", k, x))
			}
			return nil
		}
		for k, x := range v {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			if err := flattenParams(key, x, out); err != nil {
				return err
			}
		}
	case map[string]string:
		if prefix == "meta" {
			for k, x := range v {
				*out = append(*out, fmt.Sprintf("--meta=%s=%s", k, x))
			}
			return nil
		}
		if prefix == "driver.config" {
			for k, x := range v {
				*out = append(*out, fmt.Sprintf("--driver.config.%s=%s", k, x))
			}
			return nil
		}
		for k, x := range v {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			if err := flattenParams(key, x, out); err != nil {
				return err
			}
		}
	case []any:
		parts := make([]string, 0, len(v))
		for _, x := range v {
			parts = append(parts, fmt.Sprintf("%v", x))
		}
		*out = append(*out, fmt.Sprintf("--%s=[%s]", prefix, strings.Join(parts, ",")))
	case []string:
		quoted := make([]string, len(v))
		for i, x := range v {
			quoted[i] = fmt.Sprintf("%q", x)
		}
		*out = append(*out, fmt.Sprintf("--%s=[%s]", prefix, strings.Join(quoted, ",")))
	case bool:
		if v {
			*out = append(*out, fmt.Sprintf("--%s", prefix))
		}
	case nil:
		// ignore
	default:
		val := fmt.Sprintf("%v", v)
		if s, ok := v.(string); ok {
			if strings.ContainsAny(s, " \",:[]{}") {
				val = fmt.Sprintf("%q", s)
			}
		}
		*out = append(*out, fmt.Sprintf("--%s=%s", prefix, val))
	}
	return nil
}
