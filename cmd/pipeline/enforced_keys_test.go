package pipeline

import "testing"

// A user config that sets something abc must guarantee used to be accepted and
// then silently overridden by the generated file, so the run started meaning
// something other than what was written.
func TestCheckEnforcedOverrides_RefusesPlatformInvariants(t *testing.T) {
	for _, cfg := range []string{
		`workDir = "s3://somewhere-else/"`,
		`process.executor = 'local'`,
		"nomad {\n  client {\n    address = \"http://elsewhere:4646\"\n  }\n}",
		`executor.submitRateLimit = "1000/1sec"`,
		`aws.client.endpoint = "https://evil"`,
	} {
		if err := checkEnforcedOverrides(cfg, "--config x"); err == nil {
			t.Errorf("expected refusal for:\n%s", cfg)
		}
	}
}

// The whole point of the split: the shape of the run is the pipeline's to set,
// and must pass the guard untouched.
func TestCheckEnforcedOverrides_AllowsWorkloadShape(t *testing.T) {
	for _, cfg := range []string{
		`executor.queueSize = 3`,
		"executor {\n  queueSize = 3\n}",
		"nomad {\n  jobs {\n    cpuMode = \"cpu\"\n    failOnPlacementFailure = false\n  }\n}",
		"process {\n  maxRetries = 0\n  errorStrategy = 'terminate'\n}",
		`process.cpus = 4`,
	} {
		if err := checkEnforcedOverrides(cfg, "--config x"); err != nil {
			t.Errorf("must be allowed:\n%s\ngot: %v", cfg, err)
		}
	}
}

// A key named in a comment is not an assignment. Refusing on prose would block
// legitimate configs, and the generated defaults themselves explain these keys
// in comments.
func TestCheckEnforcedOverrides_IgnoresComments(t *testing.T) {
	cfg := `// workDir is managed by abc; do not set it here.
/* process.executor = "local" would be refused */
executor.queueSize = 3
`
	if err := checkEnforcedOverrides(cfg, "--config x"); err != nil {
		t.Errorf("comments must not trip the guard: %v", err)
	}
}

// The error has to say which keys and why, since the failure it replaces was
// silence.
func TestCheckEnforcedOverrides_MessageNamesKeyAndReason(t *testing.T) {
	err := checkEnforcedOverrides(`workDir = "s3://x/"`, "--config my.config")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"my.config", "workDir", "lineage", "queueSize"} {
		if !contains(err.Error(), want) {
			t.Errorf("message should mention %q:\n%s", want, err.Error())
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
