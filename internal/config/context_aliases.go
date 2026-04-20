package config

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// normalizeContextAliasList merges singular alias into aliases and dedupes.
func normalizeContextAliasList(ctx *Context) {
	if ctx == nil {
		return
	}
	if s := strings.TrimSpace(ctx.Alias); s != "" {
		ctx.Aliases = appendUniqString(ctx.Aliases, s)
		ctx.Alias = ""
	}
	ctx.Aliases = dedupeSortedStrings(ctx.Aliases)
}

func appendUniqString(slice []string, s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return slice
	}
	for _, x := range slice {
		if strings.TrimSpace(x) == s {
			return slice
		}
	}
	return append(slice, s)
}

func dedupeSortedStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// integratePerContextAliasesIntoMap registers contexts.<canon>.aliases / alias
// into ContextAliases (alias name -> immediate canonical context name).
func integratePerContextAliasesIntoMap(cfg *Config) error {
	if cfg.Contexts == nil {
		return nil
	}
	if cfg.ContextAliases == nil {
		cfg.ContextAliases = map[string]string{}
	}
	for canon, ctx := range cfg.Contexts {
		for _, a := range ctx.Aliases {
			a = strings.TrimSpace(a)
			if a == "" || a == canon {
				continue
			}
			if _, exists := cfg.Contexts[a]; exists {
				return fmt.Errorf("contexts.%q: alias %q conflicts with an existing context name", canon, a)
			}
			if prev, ok := cfg.ContextAliases[a]; ok && prev != canon {
				return fmt.Errorf("alias %q maps to both %q and %q", a, prev, canon)
			}
			cfg.ContextAliases[a] = canon
		}
	}
	return nil
}

// removeAliasKeysForCanonical deletes ContextAliases entries whose value is canon
// (direct alternate names for that context definition).
func removeAliasKeysForCanonical(c *Config, canon string) {
	if c == nil || c.ContextAliases == nil || canon == "" {
		return
	}
	for k, v := range c.ContextAliases {
		if v == canon {
			delete(c.ContextAliases, k)
		}
	}
}

// AliasesResolvingToCanon returns every alternate name that resolves to canon
// (excluding canon itself), sorted. Includes transitive names via ContextAliases.
func AliasesResolvingToCanon(c *Config, canon string) []string {
	if c == nil || canon == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, name := range allContextAndAliasKeys(c) {
		if name == canon {
			continue
		}
		if c.ResolveContextName(name) != canon {
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func allContextAndAliasKeys(c *Config) []string {
	seen := map[string]bool{}
	var keys []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		keys = append(keys, s)
	}
	if c.Contexts != nil {
		for k := range c.Contexts {
			add(k)
		}
	}
	if c.ContextAliases != nil {
		for k := range c.ContextAliases {
			add(k)
		}
	}
	sort.Strings(keys)
	return keys
}

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
			normalizeContextAuthScalarInMap(t)
			b, err := yaml.Marshal(t)
			if err != nil {
				return fmt.Errorf("contexts.%q: %w", name, err)
			}
			var ctx Context
			if err := yaml.Unmarshal(b, &ctx); err != nil {
				return fmt.Errorf("contexts.%q: %w", name, err)
			}
			normalizeContextAliasList(&ctx)
			full[name] = ctx
		default:
			return fmt.Errorf("contexts.%q: unsupported type %T (use a mapping or a string alias)", name, v)
		}
	}
	for k := range aliases {
		if _, ok := full[k]; ok {
			return fmt.Errorf("contexts.%q: cannot be both a full context and a string alias", k)
		}
	}
	for k := range full {
		if _, ok := aliases[k]; ok {
			return fmt.Errorf("contexts.%q: cannot be both a full context and a string alias", k)
		}
	}
	for k, v := range full {
		cfg.Contexts[k] = v
	}
	for k, v := range aliases {
		cfg.ContextAliases[k] = v
	}
	if err := integratePerContextAliasesIntoMap(cfg); err != nil {
		return err
	}
	if err := validateNoAliasKeyCollidesWithPrimaryName(cfg); err != nil {
		return err
	}
	return validateContextAliases(cfg)
}

// validateNoAliasKeyCollidesWithPrimaryName ensures no context primary name is
// also used as a context alias key (which would shadow resolution).
func validateNoAliasKeyCollidesWithPrimaryName(cfg *Config) error {
	if cfg == nil || cfg.ContextAliases == nil {
		return nil
	}
	for n := range cfg.Contexts {
		if _, ok := cfg.ContextAliases[n]; ok {
			return fmt.Errorf("context name %q cannot also be used as a context alias key; use a different alias or rename the context", n)
		}
	}
	return nil
}

// ResolveContextName follows context aliases and returns the canonical context
// name that has a full definition in Contexts. Returns empty if unknown or cyclic.
func (c *Config) ResolveContextName(name string) string {
	if c == nil || name == "" {
		return ""
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Primary context names always win over an alias key with the same spelling.
	if c.Contexts != nil {
		if _, ok := c.Contexts[name]; ok {
			return name
		}
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
	emittedInEmbedded := map[string]bool{}
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
		als := AliasesResolvingToCanon(c, name)
		if len(als) > 0 {
			asMap["aliases"] = als
			for _, a := range als {
				emittedInEmbedded[a] = true
			}
		} else {
			delete(asMap, "aliases")
		}
		delete(asMap, "alias")
		out[name] = asMap
	}
	var aliasNames []string
	for a := range c.ContextAliases {
		aliasNames = append(aliasNames, a)
	}
	sort.Strings(aliasNames)
	for _, alias := range aliasNames {
		if emittedInEmbedded[alias] {
			continue
		}
		if _, isDef := c.Contexts[alias]; isDef {
			continue
		}
		out[alias] = c.ContextAliases[alias]
	}
	return sortNestedMapsForYAML(out), nil
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
