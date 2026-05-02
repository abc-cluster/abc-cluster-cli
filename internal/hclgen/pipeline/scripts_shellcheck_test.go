package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/shellcheck"
)

// TestGenerate_EntrypointScriptIsValidBash extracts the embedded
// local/entrypoint.sh from the rendered HCL and runs it through the bash
// parser (always) plus shellcheck (if available). Catches syntax regressions
// before they reach Nomad.
func TestGenerate_EntrypointScriptIsValidBash(t *testing.T) {
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

	script := shellcheck.ExtractHCLHeredoc(hcl, "entrypoint.sh")
	if script == "" {
		t.Fatalf("could not extract entrypoint.sh from HCL:\n%s", hcl)
	}
	if perr := shellcheck.Parse(script); perr != nil {
		t.Fatalf("entrypoint.sh fails bash parse: %v\n--- script ---\n%s", perr, script)
	}
	out, err := shellcheck.Lint(context.Background(), script, shellcheck.Default())
	switch {
	case errors.Is(err, shellcheck.ErrShellcheckUnavailable):
		t.Logf("system shellcheck not on PATH; embedded parse passed")
	case err != nil:
		// Mirrors `abc job run --shellcheck=warn`: lint findings are advisory.
		t.Logf("shellcheck findings (advisory):\n%s", out)
	}
}
