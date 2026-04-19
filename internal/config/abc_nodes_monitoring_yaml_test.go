package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbcNodesMonitoringEnv_FromYAMLConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	raw := `version: "1.0"
active_context: enh
contexts:
  enh:
    endpoint: https://api.example.com
    access_token: tok
    cluster_type: abc-nodes
    capabilities:
      logging: true
      monitoring: true
    admin:
      services:
        nomad:
          nomad_addr: http://127.0.0.1:4646
          nomad_token: t
        loki:
          http: http://192.168.55.1:3100
        prometheus:
          http: http://192.168.55.1:9090
`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABC_CONFIG_FILE", cfgPath)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	env := AbcNodesMonitoringEnv(cfg.ActiveCtx())
	if env == nil {
		t.Fatal("expected non-nil env")
	}
	if env["ABC_NODES_CLUSTER_FLOOR"] != "enhanced" {
		t.Fatalf("floor: %#v", env)
	}
	if !strings.Contains(env["ABC_NODES_LOKI_PUSH_URL"], "/loki/api/v1/push") {
		t.Fatalf("loki push: %q", env["ABC_NODES_LOKI_PUSH_URL"])
	}
	if !strings.HasSuffix(env["ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL"], "/api/v1/write") {
		t.Fatalf("remote write: %q", env["ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL"])
	}
}
