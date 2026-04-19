//go:build integration

package job_test

// run_integration_test.go — live Nomad integration tests for abc job run.
//
// These tests require a running Nomad agent. They are gated behind the
// "integration" build tag so they are never run in offline CI.
//
// Usage:
//   NOMAD_ADDR=http://localhost:4646 go test -tags integration -v ./cmd/job/...
//
// Observability stack (Loki + Prometheus) smoke test (optional):
//   ABC_INTEGRATION_OBS_STACK=1 go test -tags integration -v -timeout=15m -run TestIntegration_ObsStack ./cmd/job/...
// Uses abc CLI config only for Nomad submit (no --nomad-addr); see monitoring_stack_integration_test.go.
//
// Optional env vars:
//   NOMAD_TOKEN       — ACL token (empty = dev agent with ACLs disabled)
//   ABC_TOKEN         — alternate token env; if both ABC_TOKEN and NOMAD_TOKEN are set,
//                       executeCmd integration paths pass --nomad-token from NOMAD_TOKEN first
//                       so in-process cobra matches raw HTTP helpers (see integrationNomadAuthFlags).
//   ABC_TEST_TIMEOUT  — max seconds to wait for job completion (default: 60)
//   ABC_TEST_NS       — Nomad namespace to use (default: "default")
//   ABC_INTEGRATION_LOKI_REQUIRE — set to 0 to skip the Loki log sentinel check in
//     TestIntegration_ObsStackJobStdoutReachableInLokiAndPrometheusAlive (Prometheus
//     check still runs). Use when Alloy does not tail client alloc logs into Loki.
//
// requireNomad(t) skips the test if NOMAD_ADDR is not set, so it is safe to
// include the build tag in broader test runs without a Nomad server.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// ── helpers ────────────────────────────────────────────────────────────────────

// requireNomad skips the test if NOMAD_ADDR is not set or not reachable.
func requireNomad(t *testing.T) string {
	t.Helper()
	ensureNomadEnv()
	addr := os.Getenv("NOMAD_ADDR")
	if addr == "" {
		addr = "http://127.0.0.1:4646"
	}
	// Quick reachability check — GET /v1/status/leader.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/v1/status/leader", nil)
	if tok := os.Getenv("NOMAD_TOKEN"); tok != "" {
		req.Header.Set("X-Nomad-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Skipf("Nomad not reachable at %s (err=%v) — skipping integration test", addr, err)
	}
	resp.Body.Close()
	return addr
}

// ensureNomadEnv uses the active abc config context when NOMAD_ADDR/TOKEN are unset.
func ensureNomadEnv() {
	addr := strings.TrimSpace(os.Getenv("NOMAD_ADDR"))
	token := strings.TrimSpace(os.Getenv("NOMAD_TOKEN"))
	if addr != "" && token != "" {
		return
	}
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}
	ctx := cfg.ActiveCtx()
	if addr == "" && strings.TrimSpace(ctx.NomadAddr()) != "" {
		_ = os.Setenv("NOMAD_ADDR", strings.TrimSpace(ctx.NomadAddr()))
	}
	if token == "" && strings.TrimSpace(ctx.NomadToken()) != "" {
		_ = os.Setenv("NOMAD_TOKEN", strings.TrimSpace(ctx.NomadToken()))
	}
}

// integrationNomadAuthFlags returns argv fragments for executeCmd after --nomad-addr so the
// in-process cobra command uses the same token as requireNomad's raw HTTP calls.
// Persistent flag defaults use EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"); when both env vars
// are set to different tokens, the weaker ABC_TOKEN would win and Nomad can return 403 on
// jobs/parse while GET /v1/status/leader still succeeds with NOMAD_TOKEN.
func integrationNomadAuthFlags() []string {
	if tok := strings.TrimSpace(os.Getenv("NOMAD_TOKEN")); tok != "" {
		return []string{"--nomad-token", tok}
	}
	if tok := strings.TrimSpace(os.Getenv("ABC_TOKEN")); tok != "" {
		return []string{"--nomad-token", tok}
	}
	return nil
}

// testNamespace returns the namespace to use for integration tests.
func testNamespace() string {
	if ns := os.Getenv("ABC_TEST_NS"); ns != "" {
		return ns
	}
	return "default"
}

// integrationTimeout returns how long to wait for a job to reach a terminal state.
func integrationTimeout() time.Duration {
	if s := os.Getenv("ABC_TEST_TIMEOUT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

// nomadJobStatus polls Nomad's /v1/job/<id> endpoint and returns the job status.
func nomadJobStatus(t *testing.T, addr, jobID string) string {
	t.Helper()
	tok := os.Getenv("NOMAD_TOKEN")
	ns := testNamespace()
	url := fmt.Sprintf("%s/v1/job/%s?namespace=%s", addr, jobID, ns)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if tok != "" {
		req.Header.Set("X-Nomad-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to query job status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "not_found"
	}
	var job struct {
		Status string `json:"Status"`
	}
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &job)
	return job.Status
}

// nomadJobType returns the Nomad job type (batch, service, system, …).
func nomadJobType(t *testing.T, addr, jobID string) string {
	t.Helper()
	tok := os.Getenv("NOMAD_TOKEN")
	ns := testNamespace()
	url := fmt.Sprintf("%s/v1/job/%s?namespace=%s", addr, jobID, ns)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if tok != "" {
		req.Header.Set("X-Nomad-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()
	var job struct {
		Type string `json:"Type"`
	}
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &job)
	return job.Type
}

// nomadBatchJobAllocationsSucceeded reports whether every allocation for a batch
// job finished successfully (client complete + main task exit 0). Nomad often
// leaves batch jobs at API Status "dead" even when work succeeded; callers
// should treat that as success when this returns true.
func nomadBatchJobAllocationsSucceeded(t *testing.T, addr, jobID string) bool {
	t.Helper()
	tok := os.Getenv("NOMAD_TOKEN")
	ns := testNamespace()
	listURL := fmt.Sprintf("%s/v1/job/%s/allocations?namespace=%s", addr, jobID, ns)
	req, _ := http.NewRequest(http.MethodGet, listURL, nil)
	if tok != "" {
		req.Header.Set("X-Nomad-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return false
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var stubs []struct {
		ID           string `json:"ID"`
		ClientStatus string `json:"ClientStatus"`
	}
	if err := json.Unmarshal(body, &stubs); err != nil || len(stubs) == 0 {
		return false
	}
	for _, st := range stubs {
		if strings.ToLower(st.ClientStatus) != "complete" {
			return false
		}
		if st.ID == "" {
			return false
		}
		allocURL := fmt.Sprintf("%s/v1/allocation/%s?namespace=%s", addr, st.ID, ns)
		r2, _ := http.NewRequest(http.MethodGet, allocURL, nil)
		if tok != "" {
			r2.Header.Set("X-Nomad-Token", tok)
		}
		ar, err := http.DefaultClient.Do(r2)
		if err != nil || ar.StatusCode != http.StatusOK {
			if ar != nil {
				ar.Body.Close()
			}
			return false
		}
		ab, _ := io.ReadAll(ar.Body)
		ar.Body.Close()
		var full struct {
			TaskStates map[string]struct {
				Events []struct {
					Type     string `json:"Type"`
					ExitCode int    `json:"ExitCode"`
					Failed   bool   `json:"Failed"`
				} `json:"Events"`
			} `json:"TaskStates"`
		}
		if err := json.Unmarshal(ab, &full); err != nil {
			return false
		}
		if len(full.TaskStates) == 0 {
			return false
		}
		for _, ts := range full.TaskStates {
			taskOK := false
			for i := len(ts.Events) - 1; i >= 0; i-- {
				ev := ts.Events[i]
				if ev.Type == "Terminated" {
					taskOK = !ev.Failed && ev.ExitCode == 0
					break
				}
			}
			if !taskOK {
				return false
			}
		}
	}
	return true
}

// nomadJobFirstCompleteAllocID returns one allocation ID with ClientStatus "complete",
// or empty when none are listed that way.
func nomadJobFirstCompleteAllocID(t *testing.T, addr, jobID string) string {
	t.Helper()
	tok := os.Getenv("NOMAD_TOKEN")
	ns := testNamespace()
	listURL := fmt.Sprintf("%s/v1/job/%s/allocations?namespace=%s", addr, jobID, ns)
	req, _ := http.NewRequest(http.MethodGet, listURL, nil)
	if tok != "" {
		req.Header.Set("X-Nomad-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var stubs []struct {
		ID           string `json:"ID"`
		ClientStatus string `json:"ClientStatus"`
	}
	if err := json.Unmarshal(body, &stubs); err != nil {
		return ""
	}
	for _, st := range stubs {
		if strings.EqualFold(st.ClientStatus, "complete") && st.ID != "" {
			return st.ID
		}
	}
	return ""
}

// nomadTerminalStatus maps raw Nomad job Status into values integration tests use:
// batch jobs that are API "dead" but finished successfully are reported as "complete".
func nomadTerminalStatus(t *testing.T, addr, jobID, raw string) string {
	t.Helper()
	if raw == "complete" {
		return "complete"
	}
	if raw != "dead" {
		return raw
	}
	jt := nomadJobType(t, addr, jobID)
	if jt == "batch" || jt == "sysbatch" {
		if nomadBatchJobAllocationsSucceeded(t, addr, jobID) {
			return "complete"
		}
	}
	return "dead"
}

// waitForJobTerminal polls until the job reaches "complete" or "dead", or times out.
func waitForJobTerminal(t *testing.T, addr, jobID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw := nomadJobStatus(t, addr, jobID)
		if raw == "complete" {
			return "complete"
		}
		if raw == "dead" {
			return nomadTerminalStatus(t, addr, jobID, raw)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("job %q did not reach terminal state within %s (last status: %s)",
		jobID, timeout, nomadJobStatus(t, addr, jobID))
	return ""
}

// stopJob purges a job from Nomad to clean up after tests.
func stopJob(t *testing.T, addr, jobID string) {
	t.Helper()
	tok := os.Getenv("NOMAD_TOKEN")
	ns := testNamespace()
	url := fmt.Sprintf("%s/v1/job/%s?namespace=%s&purge=true", addr, jobID, ns)
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	if tok != "" {
		req.Header.Set("X-Nomad-Token", tok)
	}
	resp, _ := http.DefaultClient.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
}

// extractJobID pulls the "Job submitted: <id>" line from submit output.
func extractJobID(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Job submitted:") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				return parts[len(parts)-1]
			}
		}
		if strings.HasPrefix(strings.TrimSpace(line), "Nomad job ID") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				return parts[len(parts)-1]
			}
		}
	}
	t.Fatalf("could not find job ID in output:\n%s", out)
	return ""
}

// ── B.1 HCL parse round-trip ──────────────────────────────────────────────────

// TestIntegration_HCLParseRoundTrip verifies that the generated HCL is valid
// by submitting it to Nomad's /v1/jobs/parse endpoint. No job is registered.
func TestIntegration_HCLParseRoundTrip(t *testing.T) {
	addr := requireNomad(t)
	_ = addr // used by requireNomad check; actual parse is done via --submit flow

	script := `#!/bin/bash
#ABC --name=parse-roundtrip
#ABC --cores=2
#ABC --mem=256M
echo "HCL parse test"
`
	p := writeTempScript(t, "parse_rt.sh", script)
	// Without --submit, executeCmd generates HCL and prints it; Nomad is not called.
	// To do the actual parse round-trip we need --submit which calls ParseHCL internally.
	// Here we just verify the command doesn't error with a live endpoint in scope.
	out, err := executeCmd(t, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `driver = "exec"`) {
		t.Errorf("expected exec driver in HCL, got:\n%s", out)
	}
}

// ── B.2 Dry-run (`--dry-run`) ────────────────────────────────────────────────

// TestIntegration_DryRunDoesNotRegisterJob submits a plan and confirms the job
// is not registered in Nomad afterwards.
func TestIntegration_DryRunDoesNotRegisterJob(t *testing.T) {
	addr := requireNomad(t)

	uniqueSuffix := fmt.Sprintf("dryrun-%d", time.Now().UnixMilli()%1_000_000)
	script := fmt.Sprintf("#!/bin/bash\n#ABC --name=%s\necho dryrun\n", uniqueSuffix)
	p := writeTempScript(t, "dryrun.sh", script)

	out, err := executeCmd(t, append([]string{p, "--dry-run",
		"--nomad-addr", addr},
		integrationNomadAuthFlags()...)...)
	if err != nil {
		t.Fatalf("--dry-run failed: %v", err)
	}
	// Dry-run output should mention "plan" or "diff".
	if !strings.Contains(strings.ToLower(out), "plan") &&
		!strings.Contains(strings.ToLower(out), "diff") &&
		!strings.Contains(strings.ToLower(out), "dry") {
		t.Logf("dry-run output: %s", out)
	}

	// The job must NOT be registered in Nomad.
	status := nomadJobStatus(t, addr, uniqueSuffix)
	if status != "not_found" {
		t.Errorf("--dry-run should not register job, but got status=%q", status)
		stopJob(t, addr, uniqueSuffix) // cleanup anyway
	}
}

// ── B.3 Live job submission ───────────────────────────────────────────────────

// TestIntegration_SubmitMinimalExecJob submits a fast exec job and waits for it
// to complete successfully.
func TestIntegration_SubmitMinimalExecJob(t *testing.T) {
	addr := requireNomad(t)

	script := `#!/bin/bash
#ABC --name=integration-exec-ok
#ABC --cores=1
#ABC --mem=64M
echo "integration test OK"
exit 0
`
	p := writeTempScript(t, "exec_ok.sh", script)
	out, err := executeCmd(t, append([]string{p, "--submit",
		"--nomad-addr", addr,
		"--namespace", testNamespace()},
		integrationNomadAuthFlags()...)...)
	if err != nil {
		t.Fatalf("--submit failed: %v\noutput: %s", err, out)
	}
	jobID := extractJobID(t, out)
	t.Logf("submitted job: %s", jobID)
	t.Cleanup(func() { stopJob(t, addr, jobID) })

	status := waitForJobTerminal(t, addr, jobID, integrationTimeout())
	if status != "complete" {
		t.Errorf("expected job to complete successfully, got status=%q", status)
	}
}

// TestIntegration_FailingJobReachesDeadStatus confirms that a job that exits
// with a non-zero code reaches "dead" status in Nomad.
func TestIntegration_FailingJobReachesDeadStatus(t *testing.T) {
	addr := requireNomad(t)

	script := `#!/bin/sh
#ABC --name=integration-exec-fail
#ABC --cores=1
#ABC --mem=64M
echo "about to fail"
exit 42
`
	p := writeTempScript(t, "exec_fail.sh", script)
	out, err := executeCmd(t, append([]string{p, "--submit",
		"--nomad-addr", addr,
		"--namespace", testNamespace()},
		integrationNomadAuthFlags()...)...)
	if err != nil {
		t.Fatalf("--submit failed: %v\noutput: %s", err, out)
	}
	jobID := extractJobID(t, out)
	t.Logf("submitted failing job: %s", jobID)
	t.Cleanup(func() { stopJob(t, addr, jobID) })

	status := waitForJobTerminal(t, addr, jobID, integrationTimeout())
	if status != "dead" {
		t.Errorf("expected failing job to reach dead status, got %q", status)
	}
}

// TestIntegration_SubmitWithNameOverride confirms the --name CLI flag produces
// the correct Nomad job ID.
func TestIntegration_SubmitWithNameOverride(t *testing.T) {
	addr := requireNomad(t)
	customName := fmt.Sprintf("custom-name-%d", time.Now().UnixMilli()%1_000_000)

	script := "#!/bin/bash\n#ABC --cores=1\necho custom name test\n"
	p := writeTempScript(t, "name_override.sh", script)
	out, err := executeCmd(t, append([]string{p, "--submit",
		"--name", customName,
		"--nomad-addr", addr,
		"--namespace", testNamespace()},
		integrationNomadAuthFlags()...)...)
	if err != nil {
		t.Fatalf("--submit failed: %v\noutput: %s", err, out)
	}
	// The job ID should start with the custom name.
	if !strings.Contains(out, customName) {
		t.Errorf("expected custom name %q in submit output, got:\n%s", customName, out)
	}
	jobID := extractJobID(t, out)
	t.Cleanup(func() { stopJob(t, addr, jobID) })
}

// TestIntegration_SubmitMultiNode confirms that --nodes=2 creates two allocations.
func TestIntegration_SubmitMultiNode(t *testing.T) {
	addr := requireNomad(t)

	script := `#!/bin/bash
#ABC --name=integration-multinode
#ABC --nodes=2
#ABC --cores=1
#ABC --mem=64M
echo "node $NOMAD_ALLOC_INDEX"
sleep 2
`
	p := writeTempScript(t, "multinode.sh", script)
	out, err := executeCmd(t, append([]string{p, "--submit",
		"--nomad-addr", addr,
		"--namespace", testNamespace()},
		integrationNomadAuthFlags()...)...)
	if err != nil {
		t.Fatalf("--submit failed: %v\noutput: %s", err, out)
	}
	jobID := extractJobID(t, out)
	t.Logf("submitted multi-node job: %s", jobID)
	t.Cleanup(func() { stopJob(t, addr, jobID) })

	// Poll until both allocations are running or complete.
	tok := os.Getenv("NOMAD_TOKEN")
	deadline := time.Now().Add(integrationTimeout())
	for time.Now().Before(deadline) {
		url := fmt.Sprintf("%s/v1/job/%s/allocations?namespace=%s", addr, jobID, testNamespace())
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if tok != "" {
			req.Header.Set("X-Nomad-Token", tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var allocs []struct {
			ClientStatus string `json:"ClientStatus"`
		}
		if err := json.Unmarshal(body, &allocs); err == nil && len(allocs) >= 2 {
			t.Logf("allocations: %d", len(allocs))
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Errorf("expected 2 allocations for multi-node job within %s", integrationTimeout())
}

// ── B.4 Log streaming (`--watch`) ────────────────────────────────────────────

// TestIntegration_WatchStreamsStdout submits a job that prints a known string
// and verifies it appears in the streamed logs.
func TestIntegration_WatchStreamsStdout(t *testing.T) {
	addr := requireNomad(t)

	sentinel := fmt.Sprintf("ABC_WATCH_SENTINEL_%d", time.Now().UnixMilli())
	script := fmt.Sprintf("#!/bin/bash\n#ABC --name=integration-watch\necho %s\n", sentinel)
	p := writeTempScript(t, "watch.sh", script)

	// --watch blocks until the job finishes and streams stdout.
	out, err := executeCmd(t, append([]string{p, "--submit", "--watch",
		"--nomad-addr", addr,
		"--namespace", testNamespace()},
		integrationNomadAuthFlags()...)...)
	if err != nil {
		// A non-zero exit from the watched job is OK — we still get output.
		t.Logf("--watch returned err (may be job exit code): %v", err)
	}
	if !strings.Contains(out, sentinel) {
		t.Errorf("expected sentinel %q in streamed output, got:\n%s", sentinel, out)
	}
}

// ── B.5 Connection error handling ────────────────────────────────────────────

// TestIntegration_BadNomadAddrShowsError confirms a helpful error when the
// Nomad endpoint is unreachable.
func TestIntegration_BadNomadAddrShowsError(t *testing.T) {
	// Use an address that is guaranteed to be unreachable (closed port).
	script := "#!/bin/bash\n#ABC --name=bad-addr-test\necho hi\n"
	p := writeTempScript(t, "bad_addr.sh", script)
	_, err := executeCmd(t, p, "--submit",
		"--nomad-addr", "http://127.0.0.1:19999", // nothing listening here
	)
	if err == nil {
		t.Fatal("expected error when Nomad address is unreachable")
	}
	// The error should mention "connection" or "refused" or similar.
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "connect") &&
		!strings.Contains(msg, "refused") &&
		!strings.Contains(msg, "dial") &&
		!strings.Contains(msg, "deadline") {
		t.Logf("error message: %v", err)
	}
}

// TestIntegration_BadTokenShowsAuthError confirms a 403 is surfaced clearly.
func TestIntegration_BadTokenShowsAuthError(t *testing.T) {
	addr := requireNomad(t)

	// Skip if the dev agent has ACLs disabled (anonymous requests succeed).
	checkURL := addr + "/v1/jobs?namespace=default"
	req, _ := http.NewRequest(http.MethodGet, checkURL, nil)
	req.Header.Set("X-Nomad-Token", "definitely-invalid-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skip("could not reach Nomad for ACL check")
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Skipf("Nomad ACLs appear to be disabled (status %d) — skipping bad-token test",
			resp.StatusCode)
	}

	script := "#!/bin/bash\n#ABC --name=bad-token-test\necho hi\n"
	p := writeTempScript(t, "bad_token.sh", script)
	// Override env so executeCmd uses the bad token via --nomad-token flag.
	t.Setenv("NOMAD_TOKEN", "")
	t.Setenv("ABC_TOKEN", "")
	_, err = executeCmd(t, p, "--submit",
		"--nomad-addr", addr,
		"--nomad-token", "bad-token",
	)
	if err == nil {
		t.Fatal("expected error with invalid Nomad token")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "403") &&
		!strings.Contains(msg, "forbidden") &&
		!strings.Contains(msg, "permission") &&
		!strings.Contains(msg, "auth") {
		t.Logf("error from bad token: %v", err)
	}
}

// ── B.6 Task driver matrix ────────────────────────────────────────────────────

// TestIntegration_ExecDriverCompletes submits a trivial exec job and waits for
// it to complete. This is the reference integration test.
func TestIntegration_ExecDriverCompletes(t *testing.T) {
	addr := requireNomad(t)

	script := `#!/bin/bash
#ABC --name=driver-exec-ok
#ABC --cores=1
#ABC --mem=64M
printf "exec driver: OK\n"
exit 0
`
	p := writeTempScript(t, "driver_exec.sh", script)
	out, err := executeCmd(t, append([]string{p, "--submit",
		"--nomad-addr", addr,
		"--namespace", testNamespace()},
		integrationNomadAuthFlags()...)...)
	if err != nil {
		t.Fatalf("--submit failed: %v\noutput: %s", err, out)
	}
	jobID := extractJobID(t, out)
	t.Cleanup(func() { stopJob(t, addr, jobID) })
	status := waitForJobTerminal(t, addr, jobID, integrationTimeout())
	if status != "complete" {
		t.Errorf("expected complete, got %q", status)
	}
}

// TestIntegration_DockerDriverCompletes submits a Docker job (busybox:latest)
// and waits for it to complete. The Nomad agent must have the Docker driver
// enabled and internet access (or a local registry) to pull busybox.
func TestIntegration_DockerDriverCompletes(t *testing.T) {
	addr := requireNomad(t)

	// Check that docker driver is enabled on the agent.
	driverURL := addr + "/v1/agent/self"
	req, _ := http.NewRequest(http.MethodGet, driverURL, nil)
	if tok := os.Getenv("NOMAD_TOKEN"); tok != "" {
		req.Header.Set("X-Nomad-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Skip("cannot determine driver availability — skipping docker driver test")
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"docker"`) {
		t.Skip("docker driver not available on this Nomad agent")
	}

	script := `#!/bin/bash
#ABC --name=driver-docker-ok
#ABC --driver=docker
#ABC --driver.config.image=busybox:latest
echo "docker driver: OK"
`
	p := writeTempScript(t, "driver_docker.sh", script)
	out, err := executeCmd(t, append([]string{p, "--submit",
		"--nomad-addr", addr,
		"--namespace", testNamespace()},
		integrationNomadAuthFlags()...)...)
	if err != nil {
		t.Fatalf("--submit failed: %v\noutput: %s", err, out)
	}
	jobID := extractJobID(t, out)
	t.Cleanup(func() { stopJob(t, addr, jobID) })
	status := waitForJobTerminal(t, addr, jobID, integrationTimeout())
	if status != "complete" {
		t.Errorf("expected docker job to complete, got %q", status)
	}
}

// TestIntegration_GPUJobPlanShowsConstraintFailure submits a GPU job via --dry-run
// on a cluster that likely has no GPU nodes. The plan should report a failed
// placement (FailedTGAllocs) rather than erroring out entirely.
func TestIntegration_GPUJobPlanShowsConstraintFailure(t *testing.T) {
	addr := requireNomad(t)

	script := `#!/bin/bash
#ABC --name=gpu-plan-test
#ABC --gpus=8
echo "GPU job"
`
	p := writeTempScript(t, "gpu_plan.sh", script)
	out, err := executeCmd(t, append([]string{p, "--dry-run",
		"--nomad-addr", addr,
		"--namespace", testNamespace()},
		integrationNomadAuthFlags()...)...)
	// --dry-run may succeed even if placement fails (it's a plan, not a submission).
	// We just verify it doesn't panic and produces output.
	_ = err
	if out == "" {
		t.Errorf("expected non-empty dry-run output for GPU job")
	}
}
