package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/shellcheck"
)

func oomEntrypoint(t *testing.T) string {
	t.Helper()
	spec := Spec{
		Datacenters: []string{"dc1"}, WorkDir: "s3://b/wd/", CPU: 1000, MemoryMB: 4096,
		NfVersion: "26.04.3", Repository: "nf-core/demo",
	}
	ep := shellcheck.ExtractHCLHeredoc(Generate(spec, "http://127.0.0.1:4646", "tok", "u"), "entrypoint.sh")
	if ep == "" {
		t.Fatal("could not extract entrypoint.sh")
	}
	return ep
}

// An OOM-killed head used to print one bash "Killed" line, be restarted into
// the same allocation, -resume, reach the same step and die again, while the
// job reported "running". The launch must be wrapped so its status is seen.
func TestHeadEntrypoint_LaunchStatusIsObserved(t *testing.T) {
	ep := oomEntrypoint(t)
	iOff := strings.Index(ep, "set +e\nnextflow run ")
	iRC := strings.Index(ep, "_abc_rc=$?")
	if iOff < 0 || iRC < iOff {
		t.Fatalf("nextflow run must run under set +e with its status captured after:\n%s", ep)
	}
	for _, want := range []string{
		"/sys/fs/cgroup/memory.events", // confirm, don't infer from 137
		"oom_kill",
		"ran out of memory",
		"--memory",
		`exit "$_abc_rc"`,
	} {
		if !strings.Contains(ep, want) {
			t.Errorf("entrypoint missing %q", want)
		}
	}
}

// After a CONFIRMED OOM the retry is deterministic and is stopped. The marker
// lives in the alloc dir and is written only on a confirmed OOM (a kernel kill
// the cgroup counted, or heap exhaustion, below), so a preempted head (whole
// task killed, nothing written) still resumes.
func TestHeadEntrypoint_OnlyAConfirmedOOMBlocksTheRetry(t *testing.T) {
	ep := oomEntrypoint(t)
	iCheck := strings.Index(ep, `if [ -f "$_abc_oom_marker" ]`)
	iRun := strings.Index(ep, "nextflow run ")
	if iCheck < 0 || iCheck > iRun {
		t.Error("the marker check must run before the launch")
	}
	iConfirmed := strings.Index(ep, `if [ "${_abc_oom:-0}" -gt 0 ]`)
	iWrite := strings.Index(ep, `> "$_abc_oom_marker"`)
	if iConfirmed < 0 || iWrite < 0 {
		t.Fatal("no confirmed-OOM branch or no marker write in the entrypoint")
	}
	iElse := strings.Index(ep[iConfirmed:], "else")
	if iWrite < iConfirmed || iElse < 0 || iWrite > iConfirmed+iElse {
		t.Error("the marker may only be written inside the confirmed-OOM branch")
	}
}

// The entrypoint is PID 1. When it exits the kernel kills the tee it pipes
// through before tee drains, dropping the last lines — exactly the
// out-of-memory message. Verified in nextflow/nextflow:26.04.3.
func TestHeadEntrypoint_TeeIsDrainedOnEveryExit(t *testing.T) {
	ep := oomEntrypoint(t)
	iTee := strings.Index(ep, `exec > >(tee -a "$DEBUG_LOG") 2>&1`)
	iPid := strings.Index(ep, "_abc_tee_pid=$!")
	iTrap := strings.Index(ep, "trap _abc_drain_tee EXIT")
	if iTee < 0 || iPid != iTee+len(`exec > >(tee -a "$DEBUG_LOG") 2>&1`)+1 {
		t.Error("tee's PID must be captured on the very next line, before anything else overwrites $!")
	}
	if iTrap < iPid {
		t.Error("the drain must be an EXIT trap so set -e aborts are covered too")
	}
	if !strings.Contains(ep, `-lt 50 ] && kill -0 "$_abc_tee_pid"`) {
		t.Error("the drain must be bounded")
	}
}

// runHeadAttempt runs the real preamble and epilogue around a stub nextflow
// that writes logLine to .nextflow.log and exits with rc, as the head does.
func runHeadAttempt(t *testing.T, dir, logLine string, rc int) (int, string) {
	t.Helper()
	stub := "printf '%s\\n' " + "'" + logLine + "'" + " > .nextflow.log; (exit " + strconv.Itoa(rc) + ")"
	script := "set -euo pipefail\ncd " + dir + "\n" + headOOMPreamble + "set +e\n" + stub + "\n" + headOOMEpilogue
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "NOMAD_ALLOC_DIR="+dir, "NOMAD_MEMORY_LIMIT=512")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

// With the heap sized below the container limit, a small head runs out of
// Java heap before the kernel steps in: "ERROR ~ Java heap space", exit 1.
// That used to be restarted eight times (3 restarts x 2 allocations) with no
// advice. Observed on seedling-prod with a 512 MB head.
func TestHeadEntrypoint_HeapExhaustionIsNamedAndStopsTheRetry(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	code, out := runHeadAttempt(t, dir, "java.lang.OutOfMemoryError: Java heap space", 1)
	if code != 1 {
		t.Errorf("exit = %d, want nextflow's own 1", code)
	}
	if !strings.Contains(out, "ran out of Java heap (container limit 512 MB)") || !strings.Contains(out, "--memory 1024") {
		t.Errorf("no heap message:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".abc-head-oom-killed")); err != nil {
		t.Fatalf("marker not written: %v", err)
	}

	// The retry stops before launching nextflow.
	code, out = runHeadAttempt(t, dir, "should not run", 0)
	if code != 1 || !strings.Contains(out, "previous attempt of this head ran out of memory") {
		t.Errorf("retry was not stopped (exit %d):\n%s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".nextflow.log")); strings.Contains(string(b), "should not run") {
		t.Error("nextflow ran on the retry")
	}
}

// An ordinary pipeline failure must still be retried as before.
func TestHeadEntrypoint_OrdinaryFailureIsNotTreatedAsOOM(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	code, out := runHeadAttempt(t, dir, "ERROR ~ Process `FOO` terminated with an error exit status (1)", 1)
	if code != 1 || strings.Contains(out, "FATAL") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".abc-head-oom-killed")); err == nil {
		t.Error("marker written for an ordinary failure")
	}
}
