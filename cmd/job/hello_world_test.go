package job_test

import (
	"strings"
	"testing"
)

func TestJobHelloWorldGeneratesHCL(t *testing.T) {
	out, err := executeCmd(t, "hello-world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checks := []string{
		`job "script-job-hello-world-`,
		`namespace = "default"`,
		`driver = "containerd-driver"`,
		`image   = "community.wave.seqera.io/library/hyperfine_stress-ng:4c75e186a00376f8"`,
		`hello from abc-nodes`,
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestJobHelloWorldNamespaceOverride(t *testing.T) {
	out, err := executeCmd(t, "hello-world", "--namespace", "su-mbhg-hostgen")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `namespace = "su-mbhg-hostgen"`) {
		t.Fatalf("expected namespace override in output, got:\n%s", out)
	}
}
