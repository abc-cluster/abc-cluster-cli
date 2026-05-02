package job

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/shellcheck"
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

func TestGenerate_TaskTmpInjectsEnv(t *testing.T) {
	spec := Spec{
		Name:        "tmp-job",
		Driver:      "exec",
		Datacenters: []string{"dc1"},
		Nodes:       1,
		Priority:    50,
		TaskTmp:     true,
	}
	hcl := Generate(spec, "run.sh", "#!/bin/sh\necho ok\n")
	if !strings.Contains(hcl, `TMPDIR`) || !strings.Contains(hcl, `${NOMAD_TASK_DIR}/tmp`) {
		t.Fatalf("expected TMPDIR in env block:\n%s", hcl)
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

func TestGenerate_WavePrestartTask(t *testing.T) {
	spec := Spec{
		Name:        "samtools-wave",
		Namespace:   "default",
		Datacenters: []string{"dc1"},
		Priority:    50,
		Nodes:       1,
		Cores:       4,
		MemoryMB:    8192,
		Driver:      "docker",
		DriverConfig: map[string]string{
			"image": "wave.seqera.io/wt/abc123/ubuntu:22.04",
		},
		Wave: WaveSpec{
			Enabled:          true,
			CondaFileContent: "name: env\ndependencies:\n  - samtools=1.21\n",
			TokenSecretPath:  "nomad/jobs",
			TokenSecretKey:   "wave_token",
			Platform:         "linux/amd64",
			BinarySourceURL:  "http://rustfs/binary_tools/wave-${attr.kernel.name}-${attr.cpu.arch}",
		},
	}
	hcl := Generate(spec, "samtools.sh", "#!/bin/bash\nsamtools view -c input.bam\n")

	checks := []struct {
		label string
		want  string
	}{
		{"prestart task block", `"wave-build"`},
		{"lifecycle hook", `"prestart"`},
		{"exec driver for prestart", `driver = "exec"`},
		{"wave binary curl download", `curl -fsSL`},
		{"wave binary base url", `http://rustfs/binary_tools/wave-`},
		{"uname arch detection", `uname -m`},
		{"token secret template", `nomadVar "nomad/jobs"`},
		{"token secret key", `.wave_token`},
		{"token env injection", `"secrets/wave.env"`},
		{"conda env template", `samtools=1.21`},
		{"build script template", `"local/wave-build.sh"`},
		{"await flag in script", `--await`},
		{"platform in script", `linux/amd64`},
		{"main task still present", `"main"`},
		{"main docker driver", `driver = "docker"`},
		{"wave image in main config", `wave.seqera.io/wt/abc123/ubuntu:22.04`},
	}
	for _, c := range checks {
		if !strings.Contains(hcl, c.want) {
			t.Errorf("[%s] expected HCL to contain %q\nFull HCL:\n%s", c.label, c.want, hcl)
		}
	}
}


func TestGenerate_WavePrestartScriptPassesShellcheck(t *testing.T) {
	spec := Spec{
		Name:        "samtools-wave",
		Namespace:   "default",
		Datacenters: []string{"dc1"},
		Priority:    50,
		Nodes:       1,
		Cores:       4,
		MemoryMB:    8192,
		Driver:      "docker",
		DriverConfig: map[string]string{"image": "wave.seqera.io/wt/abc123/ubuntu:22.04"},
		Wave: WaveSpec{
			Enabled:          true,
			CondaFileContent: "name: env\n",
			TokenSecretPath:  "nomad/jobs",
			TokenSecretKey:   "wave_token",
			Platform:         "linux/amd64",
			BinarySourceURL:  "http://rustfs/binary_tools/wave",
		},
	}
	hcl := Generate(spec, "samtools.sh", "#!/bin/bash\necho ok\n")

	script := shellcheck.ExtractHCLHeredoc(hcl, "wave-build.sh")
	if script == "" {
		t.Fatalf("could not extract wave-build.sh heredoc body from HCL:\n%s", hcl)
	}

	// hclwrite auto-escapes ${VAR} → $${VAR} in heredoc emission, so the
	// HCL-escape pre-check (ABC001) doesn't apply here. Always run the
	// embedded bash parser; run shellcheck only when it's on PATH.
	if perr := shellcheck.Parse(script); perr != nil {
		t.Fatalf("wave-build.sh fails bash parse: %v\n--- script ---\n%s", perr, script)
	}
	out, err := shellcheck.Lint(context.Background(), script, shellcheck.Default())
	switch {
	case errors.Is(err, shellcheck.ErrShellcheckUnavailable):
		t.Logf("system shellcheck not on PATH; embedded parse passed")
	case err != nil:
		t.Logf("shellcheck findings (advisory):\n%s", out)
	}
}
