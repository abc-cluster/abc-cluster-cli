package job

import (
	"strings"
	"testing"
)

func TestMergeJobMetaForMonitoringFloor_NilNil(t *testing.T) {
	if mergeJobMetaForMonitoringFloor(nil, nil) != nil {
		t.Fatal("expected nil meta")
	}
}

func TestMergeJobMetaForMonitoringFloor_NoStaticLeavesBaseUnchanged(t *testing.T) {
	base := map[string]string{"sample_id": "S1"}
	got := mergeJobMetaForMonitoringFloor(base, nil)
	if got["sample_id"] != "S1" {
		t.Fatalf("got %#v", got)
	}
	if _, ok := got["abc_monitoring_floor"]; ok {
		t.Fatal("did not expect abc_monitoring_floor without static env")
	}
}

func TestMergeJobMetaForMonitoringFloor_StaticAddsFloor(t *testing.T) {
	base := map[string]string{"k": "v"}
	static := map[string]string{"ABC_NODES_CLUSTER_FLOOR": "enhanced"}
	got := mergeJobMetaForMonitoringFloor(base, static)
	if got["abc_monitoring_floor"] != "enhanced" || got["k"] != "v" {
		t.Fatalf("got %#v", got)
	}
}

func TestMergeJobMetaForMonitoringFloor_StaticNoBase(t *testing.T) {
	got := mergeJobMetaForMonitoringFloor(nil, map[string]string{"ABC_NODES_CLUSTER_FLOOR": "enhanced"})
	if len(got) != 1 || got["abc_monitoring_floor"] != "enhanced" {
		t.Fatalf("got %#v", got)
	}
}

func TestGenerateHCLFromSpec_InjectsStaticEnvAndMeta(t *testing.T) {
	spec := &jobSpec{
		Name:        "meta-env-job",
		Namespace:   "default",
		Driver:      "exec",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		Meta:        map[string]string{"lab": "x"},
	}
	static := map[string]string{
		"ABC_NODES_LOKI_HTTP":     "http://10.0.0.5:3100",
		"ABC_NODES_LOKI_PUSH_URL": "http://10.0.0.5:3100/loki/api/v1/push",
	}
	hcl := generateHCLFromSpec(spec, "t.sh", "#!/bin/sh\necho\n", static)
	if !strings.Contains(hcl, `abc_monitoring_floor`) {
		t.Fatalf("missing meta abc_monitoring_floor:\n%s", hcl)
	}
	if !strings.Contains(hcl, `ABC_NODES_LOKI_PUSH_URL`) {
		t.Fatalf("missing static env:\n%s", hcl)
	}
	if !strings.Contains(hcl, `lab`) {
		t.Fatalf("expected original meta preserved:\n%s", hcl)
	}
}
