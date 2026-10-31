package pipeline

import (
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
// lives in the alloc dir and is written only on a confirmed OOM, so a
// preempted head (whole task killed, nothing written) still resumes.
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
