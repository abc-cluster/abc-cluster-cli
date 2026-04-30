package job

import (
	"strings"
	"testing"
)

func TestHelloAbcGeneratesHCL(t *testing.T) {
	spec := buildHelloAbcSpec()
	if spec.Cores != 4 {
		t.Errorf("expected 4 cores, got %d", spec.Cores)
	}
	if spec.MemoryMB < 512 {
		t.Errorf("expected at least 512 MB memory, got %d", spec.MemoryMB)
	}
	if spec.Driver == "" {
		t.Error("expected non-empty driver")
	}
	if v, ok := spec.DriverConfig["image"]; !ok || v == "" {
		t.Error("expected image in DriverConfig")
	}
}

func TestHelloAbcScriptContainsStressNG(t *testing.T) {
	spec := buildHelloAbcSpec()
	script, err := finalizeHelloAbc(spec)
	if err != nil {
		t.Fatalf("finalizeHelloAbc error: %v", err)
	}
	if !strings.Contains(script, "stress-ng") {
		t.Error("expected stress-ng in generated script")
	}
	if !strings.Contains(script, "--cpu") {
		t.Error("expected --cpu flag in stress-ng invocation")
	}
	if !strings.Contains(script, "--timeout") {
		t.Error("expected --timeout flag in stress-ng invocation")
	}
	if !strings.Contains(script, "--metrics-brief") {
		t.Error("expected --metrics-brief in stress-ng invocation")
	}
}

func TestHelloAbcMetaKeys(t *testing.T) {
	spec := buildHelloAbcSpec()
	_, err := finalizeHelloAbc(spec)
	if err != nil {
		t.Fatalf("finalizeHelloAbc error: %v", err)
	}
	required := []string{
		"abc_submission_id",
		"abc_submission_time",
		"random_scenario",
		"random_cpu",
		"random_vm",
		"random_vm_bytes",
		"random_io",
		"random_timeout_secs",
	}
	for _, k := range required {
		if _, ok := spec.Meta[k]; !ok {
			t.Errorf("expected meta key %q to be set", k)
		}
	}
}

func TestHelloAbcJobNameHasSuffix(t *testing.T) {
	spec := buildHelloAbcSpec()
	_, err := finalizeHelloAbc(spec)
	if err != nil {
		t.Fatalf("finalizeHelloAbc error: %v", err)
	}
	if !strings.Contains(spec.Name, "script-job-hello-abc-") {
		t.Errorf("unexpected job name %q", spec.Name)
	}
}

func TestHelloAbcScriptNoDuplicatePlaceholder(t *testing.T) {
	spec := buildHelloAbcSpec()
	script, err := finalizeHelloAbc(spec)
	if err != nil {
		t.Fatalf("finalizeHelloAbc error: %v", err)
	}
	if strings.Contains(script, "__STRESS_CMD__") {
		t.Error("placeholder __STRESS_CMD__ was not replaced in generated script")
	}
}

func TestHelloAbcScenarioLabelFormat(t *testing.T) {
	p := randomParams{
		CPUStressors: 2,
		VMStressors:  1,
		VMBytes:      "256M",
		IOStressors:  0,
		TimeoutSecs:  90,
	}
	label := p.scenarioLabel()
	if label != "cpu:2,vm:1:256M,io:0,t:90s" {
		t.Errorf("unexpected label %q", label)
	}
}

func TestHelloAbcStressCmdCPUOnly(t *testing.T) {
	p := randomParams{
		CPUStressors: 3,
		VMStressors:  0,
		VMBytes:      "128M",
		IOStressors:  0,
		TimeoutSecs:  60,
	}
	cmd := p.stressCmd()
	if !strings.Contains(cmd, "--cpu 3") {
		t.Error("expected --cpu 3")
	}
	if strings.Contains(cmd, "--vm") {
		t.Error("expected no --vm when VMStressors=0")
	}
	if strings.Contains(cmd, "--io") {
		t.Error("expected no --io when IOStressors=0")
	}
	if !strings.Contains(cmd, "--timeout 60s") {
		t.Error("expected --timeout 60s")
	}
}

func TestHelloAbcStressCmdAllStressors(t *testing.T) {
	p := randomParams{
		CPUStressors: 2,
		VMStressors:  2,
		VMBytes:      "512M",
		IOStressors:  2,
		TimeoutSecs:  120,
	}
	cmd := p.stressCmd()
	for _, want := range []string{"--cpu 2", "--vm 2", "--vm-bytes 512M", "--io 2", "--timeout 120s"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("expected %q in stress cmd: %s", want, cmd)
		}
	}
}

func TestHelloAbcWithDebugSleep(t *testing.T) {
	spec := buildHelloAbcSpec()
	spec.DebugSleepSecs = 30
	script, err := finalizeHelloAbc(spec)
	if err != nil {
		t.Fatalf("finalizeHelloAbc error: %v", err)
	}
	if !strings.Contains(script, "sleep 30") {
		t.Errorf("expected 'sleep 30' in script with DebugSleepSecs=30:\n%s", script)
	}
	// Sleep should appear before stress-ng
	sleepIdx := strings.Index(script, "sleep 30")
	stressIdx := strings.Index(script, "stress-ng")
	if sleepIdx > stressIdx {
		t.Error("expected sleep to appear before stress-ng in script")
	}
}

// TestParseSleepDuration verifies all supported input formats.
func TestParseSleepDuration(t *testing.T) {
	cases := []struct {
		input   string
		wantSec int
		wantErr bool
	}{
		{"60", 60, false},
		{"120", 120, false},
		{"30s", 30, false},
		{"5m", 300, false},
		{"1h", 3600, false},
		{"1h30m", 5400, false},
		{"00:02:00", 120, false},
		{"0", 0, false},
		{"", 0, true},
		{"-1", 0, true},
		{"notaduration", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseSleepDuration(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for input %q, got %d", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for input %q: %v", tc.input, err)
				return
			}
			if got != tc.wantSec {
				t.Errorf("parseSleepDuration(%q) = %d, want %d", tc.input, got, tc.wantSec)
			}
		})
	}
}
