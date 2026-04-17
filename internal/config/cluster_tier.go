package config

import (
	"fmt"
	"strings"
)

// Cluster tier values (OSS-1 / OSS-2 / commercial) per ABC platform vision.
const (
	ClusterTypeABCNodes   = "abc-nodes"
	ClusterTypeABCCluster = "abc-cluster"
	ClusterTypeABCCloud   = "abc-cloud"
)

// legacyClusterTypeABCNode was the former OSS-1 tier string; configs may still contain it.
const legacyClusterTypeABCNode = "abc-node"

// NormalizeClusterType returns the canonical tier string if s is a known value.
func NormalizeClusterType(s string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case ClusterTypeABCNodes, legacyClusterTypeABCNode:
		return ClusterTypeABCNodes, true
	case ClusterTypeABCCluster:
		return ClusterTypeABCCluster, true
	case ClusterTypeABCCloud:
		return ClusterTypeABCCloud, true
	default:
		return "", false
	}
}

// PersistActiveContextClusterType sets contexts.<active>.cluster_type after resolving aliases.
func PersistActiveContextClusterType(tier string) error {
	norm, ok := NormalizeClusterType(tier)
	if !ok {
		return fmt.Errorf("invalid cluster_type %q (want %s, %s, or %s)", tier, ClusterTypeABCNodes, ClusterTypeABCCluster, ClusterTypeABCCloud)
	}
	cfg, err := Load()
	if err != nil {
		return err
	}
	name := cfg.ActiveContext
	if name == "" {
		name = "default"
	}
	if !cfg.HasDefinedContext(name) {
		return fmt.Errorf("no saved context %q", name)
	}
	canon := cfg.ResolveContextName(name)
	ctx := cfg.Contexts[canon]
	ctx.ClusterType = norm
	cfg.Contexts[canon] = ctx
	return cfg.Save()
}
