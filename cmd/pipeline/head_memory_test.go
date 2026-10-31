package pipeline

import (
	"regexp"
	"strings"
	"testing"
)

func headSpec() *PipelineSpec {
	s := &PipelineSpec{
		Repository:  "https://github.com/CERI-KRISP/CholeraSeq",
		WorkDir:     "s3://su-sdsct-ceri/users/ottovan/workdir/x/",
		Datacenters: []string{"seedling-prod"},
	}
	s.defaults()
	s.RunTag = "ottovan-1793427111"
	return s
}

// 2048 MB OOM-killed real pipelines: CholeraSeq's head peaked at 2.28 GiB at
// its clustering stage, and the kernel killed java at ~2.07 GB resident.
func TestHeadMemory_DefaultIsEnoughForARealPipeline(t *testing.T) {
	hcl := generateHeadJobHCL(headSpec(), "http://127.0.0.1:4646", "tok", "u")
	if !regexp.MustCompile(`(?m)^\s+memory\s*=\s*4096\s*$`).MatchString(hcl) {
		t.Errorf("default head memory should be 4096 MB:\n%s", hcl)
	}
}

// The JVM's own default heap is 25% of the container. The head is sized as a
// share of the container instead of a fixed -Xmx, so the heap tracks the real
// limit even when the HCL is edited by hand after generation.
func TestHeadMemory_HeapIsAShareOfTheContainer(t *testing.T) {
	hcl := generateHeadJobHCL(headSpec(), "http://127.0.0.1:4646", "tok", "u")
	if !strings.Contains(hcl, `"-XX:MaxRAMPercentage=50"`) {
		t.Errorf("expected NXF_JVM_ARGS=-XX:MaxRAMPercentage=50 in the head env:\n%s", extractEnvBlock(hcl))
	}
	if strings.Contains(hcl, "-Xmx") {
		t.Error("a fixed -Xmx would not follow the container limit")
	}
}

// Heap sizing is the run's shape, not a platform invariant: --env replaces it.
func TestHeadMemory_UserEnvOverridesTheHeapDefault(t *testing.T) {
	s := headSpec()
	s.ExtraEnv = map[string]string{"NXF_JVM_ARGS": "-Xmx3g"}
	hcl := generateHeadJobHCL(s, "http://127.0.0.1:4646", "tok", "u")
	env := extractEnvBlock(hcl)
	if !strings.Contains(env, `"-Xmx3g"`) {
		t.Errorf("--env NXF_JVM_ARGS must win:\n%s", env)
	}
	if strings.Contains(env, "MaxRAMPercentage") {
		t.Errorf("the default must give way, not be emitted alongside:\n%s", env)
	}
}
