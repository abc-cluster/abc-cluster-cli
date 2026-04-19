package config

import (
	"testing"
	"time"
)

func TestAbcNodesClusterFloorEnhanced_CapabilitiesBase(t *testing.T) {
	c := Context{
		ClusterType: ClusterTypeABCNodes,
		Capabilities: &Capabilities{
			Logging:       false,
			Monitoring:    false,
			Observability: false,
			LastSynced:    time.Now(),
		},
		Admin: Admin{Services: AdminServices{
			Loki: &AdminFloorService{HTTP: "http://10.0.0.1:3100"},
		}},
	}
	if AbcNodesClusterFloorEnhanced(c) {
		t.Fatal("expected base when capabilities explicitly omit monitoring stack")
	}
}

func TestAbcNodesClusterFloorEnhanced_CapabilitiesEnhanced(t *testing.T) {
	c := Context{
		ClusterType: ClusterTypeABCNodes,
		Capabilities: &Capabilities{
			Logging: true,
		},
	}
	if !AbcNodesClusterFloorEnhanced(c) {
		t.Fatal("expected enhanced when capabilities.logging")
	}
}

func TestAbcNodesClusterFloorEnhanced_InferFromURLs(t *testing.T) {
	c := Context{
		ClusterType: ClusterTypeABCNodes,
		Admin: Admin{Services: AdminServices{
			Prometheus: &AdminFloorService{HTTP: "http://192.168.1.5:9090"},
		}},
	}
	if !AbcNodesClusterFloorEnhanced(c) {
		t.Fatal("expected enhanced when prometheus http is set and capabilities nil")
	}
}

func TestAbcNodesClusterFloorEnhanced_NotAbcNodes(t *testing.T) {
	c := Context{
		ClusterType: ClusterTypeABCCluster,
		Admin: Admin{Services: AdminServices{
			Loki: &AdminFloorService{HTTP: "http://x:3100"},
		}},
	}
	if AbcNodesClusterFloorEnhanced(c) {
		t.Fatal("expected false for non abc-nodes")
	}
}

func TestAbcNodesMonitoringEnv_PushAndRemoteWrite(t *testing.T) {
	c := Context{
		ClusterType: ClusterTypeABCNodes,
		Capabilities: &Capabilities{
			Logging:    true,
			Monitoring: true,
		},
		Admin: Admin{Services: AdminServices{
			Loki:       &AdminFloorService{HTTP: "http://10.0.0.2:3100/"},
			Prometheus: &AdminFloorService{HTTP: "http://10.0.0.2:9090"},
		}},
	}
	m := AbcNodesMonitoringEnv(c)
	if m == nil {
		t.Fatal("expected env map")
	}
	if m["ABC_NODES_CLUSTER_FLOOR"] != "enhanced" {
		t.Fatalf("floor: %q", m["ABC_NODES_CLUSTER_FLOOR"])
	}
	if got, want := m["ABC_NODES_LOKI_PUSH_URL"], "http://10.0.0.2:3100/loki/api/v1/push"; got != want {
		t.Fatalf("LOKI_PUSH_URL: got %q want %q", got, want)
	}
	if got, want := m["ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL"], "http://10.0.0.2:9090/api/v1/write"; got != want {
		t.Fatalf("remote write: got %q want %q", got, want)
	}
	if _, ok := m["ABC_NODES_GRAFANA_ALLOY_HTTP"]; ok {
		t.Fatal("did not expect alloy URL when observability cap false")
	}
}

func TestAbcNodesMonitoringEnv_BaseNoEnv(t *testing.T) {
	c := Context{
		ClusterType: ClusterTypeABCNodes,
		Capabilities: &Capabilities{
			Logging: false,
		},
		Admin: Admin{Services: AdminServices{
			Loki: &AdminFloorService{HTTP: "http://10.0.0.2:3100"},
		}},
	}
	if AbcNodesMonitoringEnv(c) != nil {
		t.Fatal("expected nil env on base floor even if stale loki URL remains")
	}
}
