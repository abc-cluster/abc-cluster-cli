package accounting

import (
	"fmt"
	"os"

	cfgpkg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"gopkg.in/yaml.v3"
)

// readContextBlocks pulls the accounting and emissions blocks for one named
// context out of ~/.abc/config.yaml. Missing blocks return empty maps.
//
// The accounting block recognises:
//
//	currency
//	cost.cpu_hour, cost.gpu_hour, cost.memory_gb_hour
//	cost.storage_gb_month, cost.egress_gb (Phase 2 reserved)
//
// The emissions block recognises grid_factor_gco2_per_kwh, cpu_w, gpu_w,
// memory_gb_w, pue.
func readContextBlocks(data []byte, contextName string) (map[string]string, map[string]string, error) {
	acct := map[string]string{}
	em := map[string]string{}
	if len(data) == 0 || contextName == "" {
		return acct, em, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return acct, em, fmt.Errorf("parse config yaml: %w", err)
	}
	doc := docNode(&root)
	if doc == nil {
		return acct, em, nil
	}
	contexts := mapKey(doc, "contexts")
	if contexts == nil || contexts.Kind != yaml.MappingNode {
		return acct, em, nil
	}
	ctx := mapKey(contexts, contextName)
	if ctx == nil || ctx.Kind != yaml.MappingNode {
		return acct, em, nil
	}

	if a := mapKey(ctx, "accounting"); a != nil && a.Kind == yaml.MappingNode {
		if v := mapKey(a, "currency"); v != nil && v.Kind == yaml.ScalarNode {
			acct[KeyCurrency] = v.Value
		}
		if cost := mapKey(a, "cost"); cost != nil && cost.Kind == yaml.MappingNode {
			for _, k := range []struct{ yamlKey, ourKey string }{
				{"cpu_hour", KeyCostCpuHour},
				{"gpu_hour", KeyCostGpuHour},
				{"memory_gb_hour", KeyCostMemoryGbHour},
				{"storage_gb_month", KeyCostStoragePersistentGbMonth},
				{"egress_gb", KeyCostStorageEgressGb},
			} {
				if v := mapKey(cost, k.yamlKey); v != nil && v.Kind == yaml.ScalarNode {
					acct[k.ourKey] = v.Value
				}
			}
		}
	}
	if e := mapKey(ctx, "emissions"); e != nil && e.Kind == yaml.MappingNode {
		for _, k := range []string{KeyGridFactor, KeyCpuW, KeyGpuW, KeyMemoryGbW, KeyPue} {
			if v := mapKey(e, k); v != nil && v.Kind == yaml.ScalarNode {
				em[k] = v.Value
			}
		}
	}
	return acct, em, nil
}

// SetContextBlock applies a sparse set/unset to the accounting or emissions
// block for the named context. blockName must be "accounting" or "emissions".
//
// `setKV` writes/overwrites each key. `unsetKeys` removes those keys. Keys in
// the accounting block are dot-separated (e.g. "cost.cpu_hour"); the
// emissions keys are flat (e.g. "pue").
//
// The function reads ~/.abc/config.yaml, surgically edits the YAML node tree,
// and writes the file back. Other content is preserved byte-for-byte where
// possible (yaml.v3 round-trips most structures with comments).
func SetContextBlock(blockName, contextName string, setKV map[string]string, unsetKeys []string) error {
	if blockName != "accounting" && blockName != "emissions" {
		return fmt.Errorf("unknown block %q (want accounting or emissions)", blockName)
	}
	if contextName == "" {
		return fmt.Errorf("context name is required")
	}
	path := cfgpkg.DefaultConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			data = []byte{}
		} else {
			return fmt.Errorf("read %s: %w", path, err)
		}
	}
	var root yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if root.Kind == 0 {
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{
			{Kind: yaml.MappingNode},
		}}
	}
	doc := docNode(&root)
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = []*yaml.Node{doc}
	}
	contexts := ensureMap(doc, "contexts")
	ctx := ensureMap(contexts, contextName)
	block := ensureMap(ctx, blockName)

	if blockName == "accounting" {
		applyAccountingEdits(block, setKV, unsetKeys)
	} else {
		applyEmissionsEdits(block, setKV, unsetKeys)
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	if err := writeFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func applyAccountingEdits(block *yaml.Node, setKV map[string]string, unsetKeys []string) {
	// Set.
	for k, v := range setKV {
		switch k {
		case KeyCurrency:
			setScalar(block, "currency", v)
		case KeyCostCpuHour:
			cost := ensureMap(block, "cost")
			setScalar(cost, "cpu_hour", v)
		case KeyCostGpuHour:
			cost := ensureMap(block, "cost")
			setScalar(cost, "gpu_hour", v)
		case KeyCostMemoryGbHour:
			cost := ensureMap(block, "cost")
			setScalar(cost, "memory_gb_hour", v)
		case KeyCostStoragePersistentGbMonth:
			cost := ensureMap(block, "cost")
			setScalar(cost, "storage_gb_month", v)
		case KeyCostStorageEgressGb:
			cost := ensureMap(block, "cost")
			setScalar(cost, "egress_gb", v)
		}
	}
	// Unset.
	for _, k := range unsetKeys {
		switch k {
		case KeyCurrency:
			deleteKey(block, "currency")
		case KeyCostCpuHour:
			if cost := mapKey(block, "cost"); cost != nil {
				deleteKey(cost, "cpu_hour")
			}
		case KeyCostGpuHour:
			if cost := mapKey(block, "cost"); cost != nil {
				deleteKey(cost, "gpu_hour")
			}
		case KeyCostMemoryGbHour:
			if cost := mapKey(block, "cost"); cost != nil {
				deleteKey(cost, "memory_gb_hour")
			}
		case KeyCostStoragePersistentGbMonth:
			if cost := mapKey(block, "cost"); cost != nil {
				deleteKey(cost, "storage_gb_month")
			}
		case KeyCostStorageEgressGb:
			if cost := mapKey(block, "cost"); cost != nil {
				deleteKey(cost, "egress_gb")
			}
		}
		// Tidy: remove empty cost map.
		if cost := mapKey(block, "cost"); cost != nil && len(cost.Content) == 0 {
			deleteKey(block, "cost")
		}
	}
}

func applyEmissionsEdits(block *yaml.Node, setKV map[string]string, unsetKeys []string) {
	for k, v := range setKV {
		switch k {
		case KeyGridFactor, KeyCpuW, KeyGpuW, KeyMemoryGbW, KeyPue:
			setScalar(block, k, v)
		}
	}
	for _, k := range unsetKeys {
		switch k {
		case KeyGridFactor, KeyCpuW, KeyGpuW, KeyMemoryGbW, KeyPue:
			deleteKey(block, k)
		}
	}
}

// ----- yaml.Node helpers -----

func docNode(root *yaml.Node) *yaml.Node {
	if root == nil || root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil
	}
	return root.Content[0]
}

// mapKey looks up a child of a MappingNode by string key. Returns nil if
// not found.
func mapKey(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// ensureMap returns the child mapping at key, creating it if needed.
func ensureMap(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind != yaml.MappingNode {
		parent.Kind = yaml.MappingNode
		parent.Content = nil
	}
	if existing := mapKey(parent, key); existing != nil {
		if existing.Kind == yaml.MappingNode {
			return existing
		}
		// Replace non-mapping with a fresh mapping.
		existing.Kind = yaml.MappingNode
		existing.Tag = ""
		existing.Value = ""
		existing.Content = nil
		return existing
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode
}

// setScalar writes (or replaces) a scalar key.
func setScalar(parent *yaml.Node, key, value string) {
	if parent.Kind != yaml.MappingNode {
		parent.Kind = yaml.MappingNode
		parent.Content = nil
	}
	if existing := mapKey(parent, key); existing != nil {
		existing.Kind = yaml.ScalarNode
		existing.Tag = ""
		existing.Value = value
		existing.Content = nil
		return
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
}

// deleteKey removes a key from a mapping (no-op if absent).
func deleteKey(parent *yaml.Node, key string) {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(parent.Content)-1; i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
			return
		}
	}
}

// writeFile writes data to path with the given permissions, creating
// parent directories if needed. Matches the behaviour of
// internal/config.SaveTo (best-effort, not atomic — see spec note).
func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(parentDir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == os.PathSeparator {
			return p[:i]
		}
	}
	return "."
}
