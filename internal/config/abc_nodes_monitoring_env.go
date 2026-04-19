package config

import "strings"

// AbcNodesClusterFloorEnhanced is true when the context is abc-nodes and the
// cluster exposes the optional monitoring floor (Loki / Prometheus / Alloy).
// If capabilities were synced, those booleans are authoritative. If not,
// presence of any synced admin.services.*.http URL for those services implies
// an enhanced floor (e.g. after admin services config sync only).
func AbcNodesClusterFloorEnhanced(c Context) bool {
	if !c.IsABCNodesCluster() {
		return false
	}
	if caps := c.Capabilities; caps != nil {
		return caps.Logging || caps.Monitoring || caps.Observability
	}
	return abcNodesMonitoringURLHint(&c.Admin.Services)
}

func abcNodesMonitoringURLHint(s *AdminServices) bool {
	for _, svc := range []string{"loki", "prometheus", "grafana_alloy"} {
		if v, ok := GetAdminFloorField(s, svc, "http"); ok && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// AbcNodesMonitoringEnv returns literal environment variables to inject into
// Nomad batch tasks (abc job run, abc pipeline run) on an enhanced abc-nodes
// floor. Returns nil for non–abc-nodes contexts, base clusters, or when no
// relevant admin.services URLs are set.
func AbcNodesMonitoringEnv(c Context) map[string]string {
	if !c.IsABCNodesCluster() || !AbcNodesClusterFloorEnhanced(c) {
		return nil
	}
	caps := c.Capabilities
	s := &c.Admin.Services

	out := make(map[string]string)
	out["ABC_NODES_CLUSTER_FLOOR"] = "enhanced"

	if caps == nil || caps.Logging {
		if v, ok := GetAdminFloorField(s, "loki", "http"); ok {
			if base := strings.TrimRight(strings.TrimSpace(v), "/"); base != "" {
				out["ABC_NODES_LOKI_HTTP"] = base
				out["ABC_NODES_LOKI_PUSH_URL"] = base + "/loki/api/v1/push"
			}
		}
	}
	if caps == nil || caps.Monitoring {
		if v, ok := GetAdminFloorField(s, "prometheus", "http"); ok {
			if base := strings.TrimRight(strings.TrimSpace(v), "/"); base != "" {
				out["ABC_NODES_PROMETHEUS_HTTP"] = base
				out["ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL"] = base + "/api/v1/write"
			}
		}
	}
	if caps == nil || caps.Observability {
		if v, ok := GetAdminFloorField(s, "grafana_alloy", "http"); ok {
			if u := strings.TrimSpace(v); u != "" {
				out["ABC_NODES_GRAFANA_ALLOY_HTTP"] = u
			}
		}
	}

	if len(out) == 1 { // only ABC_NODES_CLUSTER_FLOOR
		return nil
	}
	return out
}
