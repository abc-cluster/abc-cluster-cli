package appgen

import (
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// MarshalCanonical serialises the spec to YAML with a deterministic key order —
// `version` first, then the documented field order, with `env` keys sorted — so
// any programmatic write is stable and diffable. This mirrors the config.yaml
// ordered serializer (internal/config/config_yaml_ordered.go): version-first,
// sorted nested maps. Call after ApplyDefaults for the resolved form.
//
// It emits no comments (unlike the `abc app init` scaffold template), so it is
// the canonical/normalised form, not a replacement for the hand-authored,
// commented descriptor.
func (s *Spec) MarshalCanonical() ([]byte, error) {
	// Tag !!str so values stay strings on round-trip — without it a float-like
	// value such as the version "1.0" would be emitted unquoted and re-read as a
	// float (yaml.v3 then quotes it as needed, e.g. version: "1.0").
	scalar := func(v string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	}
	intNode := func(n int) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(n)}
	}

	root := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k string, v *yaml.Node) { root.Content = append(root.Content, scalar(k), v) }

	// Fixed top-level order — version first (mirrors config.yaml).
	add("version", scalar(normalizeSpecVersion(s.Version)))
	add("name", scalar(s.Name))
	add("project", scalar(s.Project))
	add("framework", scalar(s.NormFramework()))
	add("image", scalar(s.Image))
	if s.Port != 0 {
		add("port", intNode(s.Port))
	}
	if strings.TrimSpace(s.Health) != "" {
		add("health", scalar(s.Health))
	}
	if strings.TrimSpace(s.Access) != "" {
		add("access", scalar(strings.ToLower(strings.TrimSpace(s.Access))))
	}
	if strings.TrimSpace(s.Exposure) != "" {
		add("exposure", scalar(s.NormExposure()))
	}
	if s.Replicas != 0 {
		add("replicas", intNode(s.Replicas))
	}
	if s.Resources.CPU != 0 || s.Resources.Memory != 0 {
		r := &yaml.Node{Kind: yaml.MappingNode}
		if s.Resources.CPU != 0 {
			r.Content = append(r.Content, scalar("cpu"), intNode(s.Resources.CPU))
		}
		if s.Resources.Memory != 0 {
			r.Content = append(r.Content, scalar("memory"), intNode(s.Resources.Memory))
		}
		add("resources", r)
	}
	if len(s.Env) > 0 {
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic env order
		e := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range keys {
			e.Content = append(e.Content, scalar(k), scalar(s.Env[k]))
		}
		add("env", e)
	}
	if len(s.Data) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, d := range s.Data {
			acc := strings.TrimSpace(d.Access)
			if acc == "" {
				acc = AccessRead
			}
			m := &yaml.Node{Kind: yaml.MappingNode}
			m.Content = append(m.Content,
				scalar("bucket"), scalar(d.Bucket),
				scalar("access"), scalar(acc),
			)
			seq.Content = append(seq.Content, m)
		}
		add("data", seq)
	}

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return yaml.Marshal(doc)
}
