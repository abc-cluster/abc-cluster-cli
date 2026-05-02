package module

import (
	"context"
	"errors"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/shellcheck"
)

// validateBashScript mirrors the production `abc job run --shellcheck=warn`
// behavior: bash parse errors hard-fail the test (the script is broken and
// would never run), shellcheck findings log as informational (would only
// gate submission under --shellcheck=error).
func validateBashScript(t *testing.T, hcl, destName string) {
	t.Helper()
	script := shellcheck.ExtractHCLHeredoc(hcl, destName)
	if script == "" {
		t.Fatalf("could not extract %s from HCL:\n%s", destName, hcl)
	}
	if perr := shellcheck.Parse(script); perr != nil {
		t.Fatalf("%s fails bash parse: %v\n--- script ---\n%s", destName, perr, script)
	}
	out, err := shellcheck.Lint(context.Background(), script, shellcheck.Default())
	switch {
	case errors.Is(err, shellcheck.ErrShellcheckUnavailable):
		t.Logf("[%s] system shellcheck not on PATH; embedded parse passed", destName)
	case err != nil:
		t.Logf("[%s] shellcheck findings (advisory):\n%s", destName, out)
	}
}

func TestGenerateEmit_EmitScriptIsValidBash(t *testing.T) {
	spec := EmitSpec{
		JobName:            "ss-emit-nf-core-fastp-abcd1234",
		Module:             "nf-core/fastp",
		TaskDriver:         "docker",
		PipelineGenRepo:    "abc-cluster/nf-pipeline-gen",
		PipelineGenVersion: "latest",
		GitHubToken:        "ghp_test",
		NfVersion:          "25.10.4",
		Datacenters:        []string{"dc1"},
	}
	hcl := GenerateEmit(spec, "http://nomad.test", "nomad-tok", "uuid-abc")
	validateBashScript(t, hcl, "emit.sh")
}

// minimalRunSpec returns the smallest spec that drives Generate to emit
// both generate.sh and run.sh scripts.
func minimalRunSpec() Spec {
	return Spec{
		JobName:            "module-run-abcd",
		Module:             "nf-core/fastp",
		TaskDriver:         "docker",
		WorkDir:            "/work/nf",
		HostVolume:         "abc-shared",
		OutputPrefix:       "/work/nf/out",
		PipelineGenRepo:    "abc-cluster/nf-pipeline-gen",
		PipelineGenVersion: "latest",
		GitHubToken:        "ghp_test",
		CPU:                1000,
		MemoryMB:           2048,
		NfVersion:          "25.10.4",
		NfPluginVersion:    "0.4.0-edge3",
		Datacenters:        []string{"dc1"},
		Profile:            "test",
	}
}

func TestGenerate_GenerateScriptIsValidBash(t *testing.T) {
	hcl := Generate(minimalRunSpec(), "http://nomad.test", "tok", "uuid-1")
	validateBashScript(t, hcl, "generate.sh")
}

func TestGenerate_RunScriptIsValidBash(t *testing.T) {
	hcl := Generate(minimalRunSpec(), "http://nomad.test", "tok", "uuid-2")
	validateBashScript(t, hcl, "run.sh")
}
