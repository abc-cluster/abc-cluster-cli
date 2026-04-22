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

func TestGenerate_ContainerdDriverUsesShForScriptRunner(t *testing.T) {
	spec := Spec{
		Name:        "ctrd-job",
		Driver:      "containerd-driver",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		WalltimeSecs: 120,
	}
	hcl := Generate(spec, "run.sh", "#!/bin/sh\necho ok\n")
	if !strings.Contains(hcl, `= "timeout"`) {
		t.Fatalf("expected walltime timeout wrapper:\n%s", hcl)
	}
	if !strings.Contains(hcl, `"120"`) || !strings.Contains(hcl, `"/bin/sh"`) || !strings.Contains(hcl, `"$${NOMAD_TASK_DIR}/run.sh"`) {
		t.Fatalf("expected timeout args with /bin/sh and $${NOMAD_TASK_DIR}/run.sh (Nomad-escaped), got:\n%s", hcl)
	}
	if !strings.Contains(hcl, `destination = "local/run.sh"`) {
		t.Fatalf("expected templated script under local/, got:\n%s", hcl)
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

func TestScriptArgForDriver(t *testing.T) {
	if got := ScriptArgForDriver("exec", "run.sh"); got != "local/run.sh" {
		t.Fatalf("exec: got %q", got)
	}
	if got := ScriptArgForDriver("docker", "run.sh"); got != "${NOMAD_TASK_DIR}/run.sh" {
		t.Fatalf("docker: got %q", got)
	}
	if got := ScriptArgForDriver("containerd-driver", "x.sh"); got != "${NOMAD_TASK_DIR}/x.sh" {
		t.Fatalf("containerd-driver: got %q", got)
	}
}
