package pipeline

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A task's workload identity can read only nomad/jobs, nomad/jobs/<job> and
// nomad/jobs/<job>/<group>/<task>. The head used to template
// nomad/jobs/secrets/<NAME>, which only a job named "secrets" can read, so every
// head without baked credentials blocked on "Missing: nomad.var.block".
func TestHeadCredsTemplateReadsTheNamespaceRoot(t *testing.T) {
	spec := Spec{
		Datacenters: []string{"dc1"}, WorkDir: "s3://b/wd/", CPU: 1000, MemoryMB: 4096,
		NfVersion: "26.04.3", Repository: "nf-core/demo",
	}
	hcl := Generate(spec, "http://127.0.0.1:4646", "tok", "u")
	if strings.Contains(hcl, "nomad/jobs/secrets") {
		t.Error("head still templates nomad/jobs/secrets/*, which no head's identity can read")
	}
	if !strings.Contains(hcl, `{{- with nomadVar "nomad/jobs" -}}`) {
		t.Errorf("head does not template the namespace-root nomad/jobs variable:\n%s", hcl)
	}
	for _, k := range []string{
		"{{- if .AWS_ACCESS_KEY_ID }}", "AWS_ACCESS_KEY_ID={{ .AWS_ACCESS_KEY_ID }}",
		"{{- if .AWS_SECRET_ACCESS_KEY }}", "AWS_SECRET_ACCESS_KEY={{ .AWS_SECRET_ACCESS_KEY }}",
		"[head] FATAL: no S3 credentials",
	} {
		if !strings.Contains(hcl, k) {
			t.Errorf("template missing %q", k)
		}
	}

	spec.SkipNomadVarCreds = true
	hcl = Generate(spec, "http://127.0.0.1:4646", "tok", "u")
	if strings.Contains(hcl, "secrets/aws.env") {
		t.Error("SkipNomadVarCreds must omit the credentials template")
	}
	if strings.Contains(hcl, "no S3 credentials") {
		t.Error("baked credentials must not get the variable preflight")
	}
}

func TestHeadCredsPreflight(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	run := func(env ...string) (int, string) {
		cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+headCredsPreflight+"echo launched\n")
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "NOMAD_NAMESPACE=su-x"}, env...)
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		}
		return 0, string(out)
	}
	if code, out := run(); code != 1 || !strings.Contains(out, "nomad/jobs in namespace su-x") || strings.Contains(out, "launched") {
		t.Errorf("no credentials: exit %d\n%s", code, out)
	}
	if code, out := run("AWS_ACCESS_KEY_ID=k"); code != 1 || strings.Contains(out, "launched") {
		t.Errorf("half the credentials: exit %d\n%s", code, out)
	}
	if code, out := run("AWS_ACCESS_KEY_ID=k", "AWS_SECRET_ACCESS_KEY=s"); code != 0 || !strings.Contains(out, "launched") {
		t.Errorf("credentials present: exit %d\n%s", code, out)
	}
}
