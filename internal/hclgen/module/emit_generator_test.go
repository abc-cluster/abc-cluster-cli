package module

import (
	"strings"
	"testing"
)

func TestGenerateEmit_ContainsExpectedSurfaces(t *testing.T) {
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

	checks := []string{
		`job "ss-emit-nf-core-fastp-abcd1234"`,
		`type        = "batch"`,
		`task "emit"`,
		`image   = "nextflow/nextflow:25.10.4"`,
		// JarFetch + ModulesFetch templates landed
		`pipeline-gen.jar`,
		`https://api.github.com/repos/nf-core/modules/commits/master`,
		// emit-specific bits
		`--emit-samplesheet`,
		// Variable publish at the job-namespaced path
		"nomad/jobs/ss-emit-nf-core-fastp-abcd1234/samplesheet/result",
		// Lighter resources than module run
		`memory = 512`,
	}
	for _, want := range checks {
		if !strings.Contains(hcl, want) {
			t.Errorf("expected %q in HCL, missing\n--- HCL ---\n%s", want, hcl)
		}
	}

	// Should NOT contain run-task surfaces (this is the emit job, not a full run).
	negChecks := []string{
		`task "nextflow"`,
		`task "generate"`,
		`nextflow run main.nf`,
	}
	for _, dontWant := range negChecks {
		if strings.Contains(hcl, dontWant) {
			t.Errorf("did not expect %q in emit HCL", dontWant)
		}
	}
}

func TestVariablePathForEmit_NamespacedByJob(t *testing.T) {
	got := VariablePathForEmit("ss-emit-foo")
	want := "nomad/jobs/ss-emit-foo/samplesheet/result"
	if got != want {
		t.Errorf("VariablePathForEmit = %q, want %q", got, want)
	}
}
