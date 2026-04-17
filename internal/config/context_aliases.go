package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// mergeContextsFromRaw parses contexts.<name> entries: either a mapping (full Context)
// or a string scalar (alias to another context name).
// Full definitions are applied before aliases so validation can resolve targets.
func mergeContextsFromRaw(raw map[string]interface{}, cfg *Config) error {
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	if cfg.ContextAliases == nil {
		cfg.ContextAliases = map[string]string{}
	}
	ctxNode, ok := raw["contexts"]
	if !ok || ctxNode == nil {
		return nil
	}
	cm, ok := ctxNode.(map[string]interface{})
	if !ok {
		return fmt.Errorf("contexts: expected a mapping, got %T", ctxNode)
	}
	full := make(map[string]Context)
	aliases := make(map[string]string)
	for name, v := range cm {
		switch t := v.(type) {
		case string:
			target := strings.TrimSpace(t)
			if target == "" {
				return fmt.Errorf("contexts.%q: alias target is empty", name)
			}
			aliases[name] = target
		case map[string]interface{}:
			b, err := yaml.Marshal(t)
			if err != nil {
				return fmt.Errorf("contexts.%q: %w", name, err)
			}
			var ctx Context
			if err := yaml.Unmarshal(b, &ctx); err != nil {
				return fmt.Errorf("contexts.%q: %w", name, err)
			}
			full[name] = ctx
		default:
			return fmt.Errorf("contexts.%q: unsupported type %T (use a mapping or a string alias)", name, v)
		}
	}
	for k, v := range full {
		cfg.Contexts[k] = v
	}
	for k, v := range aliases {
		cfg.ContextAliases[k] = v
	}
	return validateContextAliases(cfg)
}

// ResolveContextName follows context aliases and returns the canonical context
// name that has a full definition in Contexts. Returns empty if unknown or cyclic.
func (c *Config) ResolveContextName(name string) string {
	if c == nil || name == "" {
		return ""
	}
	seen := map[string]bool{}
	for name != "" {
		if seen[name] {
			return ""
		}
		seen[name] = true
		if c.ContextAliases == nil {
			break
		}
		target, ok := c.ContextAliases[name]
		if !ok {
			break
		}
		name = strings.TrimSpace(target)
	}
	if name == "" {
		return ""
	}
	if c.Contexts == nil {
		return ""
	}
	if _, ok := c.Contexts[name]; !ok {
		return ""
	}
	return name
}

// ContextNamed returns the resolved Context for a logical name (alias or canonical).
func (c *Config) ContextNamed(name string) (Context, bool) {
	key := c.ResolveContextName(name)
	if key == "" {
		return Context{}, false
	}
	ctx, ok := c.Contexts[key]
	return ctx, ok
}

// HasDefinedContext reports whether name refers to a real context (via alias or direct).
func (c *Config) HasDefinedContext(name string) bool {
	_, ok := c.ContextNamed(name)
	return ok
}

// AllContextEntryNames returns sorted union of alias keys and defined context keys.
func (c *Config) AllContextEntryNames() []string {
	seen := map[string]bool{}
	var out []string
	if c.ContextAliases != nil {
		for k := range c.ContextAliases {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	if c.Contexts != nil {
		for k := range c.Contexts {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func validateContextAliases(cfg *Config) error {
	if cfg.ContextAliases == nil {
		return nil
	}
	for alias := range cfg.ContextAliases {
		if cfg.ResolveContextName(alias) == "" {
			return fmt.Errorf("contexts.%q: invalid alias or target context missing", alias)
		}
	}
	return nil
}

// marshalContextsYAML builds the YAML mapping for contexts (definitions + string aliases).
func (c *Config) marshalContextsYAML() (map[string]interface{}, error) {
	out := make(map[string]interface{})
	defNames := sortedContextNames(c.Contexts)
	for _, name := range defNames {
		ctx := c.Contexts[name]
		b, err := yaml.Marshal(&ctx)
		if err != nil {
			return nil, err
		}
		var asMap map[string]interface{}
		if err := yaml.Unmarshal(b, &asMap); err != nil {
			return nil, err
		}
		out[name] = asMap
	}
	var aliasNames []string
	for a := range c.ContextAliases {
		aliasNames = append(aliasNames, a)
	}
	sort.Strings(aliasNames)
	for _, alias := range aliasNames {
		out[alias] = c.ContextAliases[alias]
	}
	return out, nil
}

func yamlString(v interface{}) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// parseConfigYAML parses the full config document (version, active_context, defaults, contexts).
func parseConfigYAML(data []byte) (*Config, error) {
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root == nil {
		root = map[string]interface{}{}
	}
	cfg := &Config{
		Version:        yamlString(root["version"]),
		ActiveContext:  strings.TrimSpace(yamlString(root["active_context"])),
		Contexts:       map[string]Context{},
		ContextAliases: map[string]string{},
	}
	if d, ok := root["defaults"]; ok && d != nil {
		b, err := yaml.Marshal(d)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(b, &cfg.Defaults); err != nil {
			return nil, err
		}
	}
	if err := mergeContextsFromRaw(root, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
