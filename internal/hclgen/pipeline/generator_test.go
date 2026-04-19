package pipeline

import (
	"strings"
	"testing"
)

func TestGenerate_StaticEnvAndMonitoringMeta(t *testing.T) {
	spec := Spec{
		Datacenters:     []string{"dc1"},
		WorkDir:         "/work/nextflow-work",
		CPU:             1000,
		MemoryMB:        2048,
		NfVersion:       "25.10.4",
		NfPluginVersion: "0.4.0-edge3",
		Repository:      "nextflow-io/hello",
		StaticEnv: map[string]string{
			"ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL": "http://10.0.0.1:9090/api/v1/write",
		},
	}
	hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "run-uuid-test")
	if !strings.Contains(hcl, `abc_monitoring_floor`) || !strings.Contains(hcl, `enhanced`) {
		t.Fatalf("expected job meta abc_monitoring_floor when StaticEnv set:\n%s", hcl)
	}
	if !strings.Contains(hcl, `ABC_NODES_PROMETHEUS_REMOTE_WRITE_URL`) {
		t.Fatalf("expected static env in task env:\n%s", hcl)
	}
	if !strings.Contains(hcl, `run_uuid`) || !strings.Contains(hcl, `run-uuid-test`) {
		t.Fatalf("expected run_uuid meta:\n%s", hcl)
	}
}

func TestGenerate_NoStaticEnv_NoMonitoringMeta(t *testing.T) {
	spec := Spec{
		Datacenters:     []string{"dc1"},
		WorkDir:         "/work/nextflow-work",
		CPU:             1000,
		MemoryMB:        2048,
		NfVersion:       "25.10.4",
		NfPluginVersion: "0.4.0-edge3",
		Repository:      "nextflow-io/hello",
	}
	hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "uuid-2")
	if strings.Contains(hcl, `abc_monitoring_floor`) {
		t.Fatalf("did not expect abc_monitoring_floor without StaticEnv:\n%s", hcl)
	}
}
