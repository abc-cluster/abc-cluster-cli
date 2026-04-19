package pipeline

import (
	"strings"
	"testing"
)

func TestGenerateHeadJobHCLWithStaticEnv_MetaAndEnv(t *testing.T) {
	spec := &PipelineSpec{
		Repository:      "nextflow-io/hello",
		WorkDir:         "/work/nextflow-work",
		Datacenters:     []string{"dc1"},
		NfVersion:       "25.10.4",
		NfPluginVersion: "0.4.0-edge3",
	}
	spec.defaults()
	static := map[string]string{
		"ABC_NODES_GRAFANA_ALLOY_HTTP": "http://10.0.0.3:12345",
	}
	hcl := generateHeadJobHCLWithStaticEnv(spec, "http://127.0.0.1:4646", "secret-token", "uuid-abc", static)
	if !strings.Contains(hcl, `abc_monitoring_floor`) {
		t.Fatalf("missing job meta:\n%s", hcl)
	}
	if !strings.Contains(hcl, `ABC_NODES_GRAFANA_ALLOY_HTTP`) {
		t.Fatalf("missing static env:\n%s", hcl)
	}
	if !strings.Contains(hcl, `secret-token`) {
		t.Fatalf("expected NOMAD_TOKEN in env:\n%s", hcl)
	}
}
