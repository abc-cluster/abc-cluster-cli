package job

import (
	"strings"
	"testing"
)

func TestGenerate_StaticEnvOnlyCreatesEnvBlock(t *testing.T) {
	spec := Spec{
		Name:        "static-env-job",
		Driver:      "exec",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		StaticEnv: map[string]string{
			"ABC_NODES_CLUSTER_FLOOR": "enhanced",
			"ABC_NODES_LOKI_PUSH_URL": "http://127.0.0.1:3100/loki/api/v1/push",
		},
	}
	script := "#!/bin/sh\necho ok\n"
	hcl := Generate(spec, "run.sh", script)
	if !strings.Contains(hcl, `ABC_NODES_CLUSTER_FLOOR`) || !strings.Contains(hcl, `ABC_NODES_LOKI_PUSH_URL`) {
		t.Fatalf("expected static env keys in HCL:\n%s", hcl)
	}
	if !strings.Contains(hcl, `env {`) {
		t.Fatalf("expected env block when only StaticEnv is set:\n%s", hcl)
	}
}

func TestGenerate_StaticEnvSortedKeysStable(t *testing.T) {
	spec := Spec{
		Name:        "order-job",
		Driver:      "exec",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		StaticEnv: map[string]string{
			"ZZ_LAST": "z",
			"AA_FIRST": "a",
		},
	}
	hcl := Generate(spec, "x.sh", "#!/bin/sh\n")
	iZZ := strings.Index(hcl, `ZZ_LAST`)
	iAA := strings.Index(hcl, `AA_FIRST`)
	if iAA == -1 || iZZ == -1 {
		t.Fatal("missing env keys")
	}
	if iAA >= iZZ {
		t.Fatalf("expected sorted key order AA_FIRST before ZZ_LAST; aa=%d zz=%d", iAA, iZZ)
	}
}
