package compute

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const (
	outputTable = "table"
	outputJSON  = "json"
)

func addStructuredOutputFlags(cmd *cobra.Command, defaultOutput string) {
	cmd.Flags().String("output", defaultOutput, "Output format: table or json")
	cmd.Flags().String("json-path", "", "When --output=json, print only the selected JSON path")
}

func writeStructuredOutput(cmd *cobra.Command, payload any, writeTable func(io.Writer)) error {
	out := cmd.OutOrStdout()
	format, _ := cmd.Flags().GetString("output")
	format = strings.TrimSpace(strings.ToLower(format))
	jsonPath, _ := cmd.Flags().GetString("json-path")
	jsonPath = strings.TrimSpace(jsonPath)

	switch format {
	case outputTable:
		if jsonPath != "" {
			return fmt.Errorf("--json-path requires --output=json")
		}
		writeTable(out)
		return nil
	case outputJSON:
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal output as json: %w", err)
		}
		if jsonPath == "" {
			_, _ = out.Write(b)
			_, _ = fmt.Fprintln(out)
			return nil
		}
		selected, err := selectJSONPath(b, jsonPath)
		if err != nil {
			return err
		}
		switch v := selected.(type) {
		case string:
			_, _ = fmt.Fprintln(out, v)
		default:
			j, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal selected json path value: %w", err)
			}
			_, _ = out.Write(j)
			_, _ = fmt.Fprintln(out)
		}
		return nil
	default:
		return fmt.Errorf("invalid --output %q (expected table or json)", format)
	}
}

func selectJSONPath(doc []byte, path string) (any, error) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return nil, fmt.Errorf("--json-path cannot be empty")
	}

	var root any
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("decode json output for path lookup: %w", err)
	}

	tokens, err := tokenizePath(path)
	if err != nil {
		return nil, err
	}

	cur := root
	for _, tok := range tokens {
		switch t := tok.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("json path %q: expected object before key %q", path, t)
			}
			next, ok := m[t]
			if !ok {
				return nil, fmt.Errorf("json path %q did not match any value", path)
			}
			cur = next
		case int:
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("json path %q: expected array before index %d", path, t)
			}
			if t < 0 || t >= len(arr) {
				return nil, fmt.Errorf("json path %q index %d out of range", path, t)
			}
			cur = arr[t]
		default:
			return nil, fmt.Errorf("json path %q: unsupported token type", path)
		}
	}

	return cur, nil
}

func tokenizePath(path string) ([]any, error) {
	parts := strings.Split(path, ".")
	out := make([]any, 0, len(parts)*2)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("invalid --json-path %q", path)
		}
		for len(part) > 0 {
			idx := strings.IndexByte(part, '[')
			if idx == -1 {
				out = append(out, part)
				break
			}
			if idx > 0 {
				out = append(out, part[:idx])
			}
			part = part[idx+1:]
			end := strings.IndexByte(part, ']')
			if end == -1 {
				return nil, fmt.Errorf("invalid --json-path %q: missing ]", path)
			}
			indexStr := strings.TrimSpace(part[:end])
			if indexStr == "" {
				return nil, fmt.Errorf("invalid --json-path %q: empty [] index", path)
			}
			idxVal, err := strconv.Atoi(indexStr)
			if err != nil {
				return nil, fmt.Errorf("invalid --json-path %q: non-numeric index %q", path, indexStr)
			}
			out = append(out, idxVal)
			part = part[end+1:]
		}
	}
	return out, nil
}
