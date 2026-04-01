package job

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// loadParamsFile reads a YAML params file and converts it to a slice of
// "--key=value" directive strings compatible with applyDirective. Nested keys
// are flattened with dot notation: foo.bar=baz → --foo.bar=baz.
func loadParamsFile(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse params file as YAML: %w", err)
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
