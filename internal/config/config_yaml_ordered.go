package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// normalizeConfigFileVersionForSave maps legacy empty or "1" to CurrentVersion.
func normalizeConfigFileVersionForSave(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "1" {
		return CurrentVersion
	}
	return v
}

func sortNestedMapsForYAML(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]interface{}, len(m))
	for _, k := range keys {
		out[k] = sortYAMLValue(m[k])
	}
	return out
}

func sortYAMLValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return sortNestedMapsForYAML(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			out[i] = sortYAMLValue(e)
		}
		return out
	default:
		return v
	}
}

// mapToYAMLMappingSorted marshals a map with lexicographically sorted keys at
// every object level, returning the inner mapping node (not a document).
func mapToYAMLMappingSorted(m map[string]interface{}) (*yaml.Node, error) {
	m = sortNestedMapsForYAML(m)
	data, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("yaml: expected document root")
	}
	inner := doc.Content[0]
	if inner.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("yaml: expected mapping at document root, got %v", inner.Kind)
	}
	return inner, nil
}

func plainStr(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: s}
}

// marshalConfigDocumentYAML emits a document with fixed top-level key order:
// version, active_context, contexts, then defaults when non-empty.
func (c *Config) marshalConfigDocumentYAML() ([]byte, error) {
	contextsOut, err := c.marshalContextsYAML()
	if err != nil {
		return nil, err
	}
	ctxNode, err := mapToYAMLMappingSorted(contextsOut)
	if err != nil {
		return nil, fmt.Errorf("contexts: %w", err)
	}

	root := &yaml.Node{Kind: yaml.MappingNode}
	root.Content = append(root.Content,
		plainStr("version"), plainStr(c.Version),
		plainStr("active_context"), plainStr(c.ActiveContext),
		plainStr("contexts"), ctxNode,
	)

	if c.Defaults.Output != "" || c.Defaults.Region != "" {
		defBytes, err := yaml.Marshal(&c.Defaults)
		if err != nil {
			return nil, fmt.Errorf("defaults: %w", err)
		}
		var defMap map[string]interface{}
		if err := yaml.Unmarshal(defBytes, &defMap); err != nil {
			return nil, fmt.Errorf("defaults map: %w", err)
		}
		defNode, err := mapToYAMLMappingSorted(defMap)
		if err != nil {
			return nil, fmt.Errorf("defaults node: %w", err)
		}
		root.Content = append(root.Content, plainStr("defaults"), defNode)
	}

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return yaml.Marshal(doc)
}
