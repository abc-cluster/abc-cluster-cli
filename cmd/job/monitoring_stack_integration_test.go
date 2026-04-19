//go:build integration

package job_test

// monitoring_stack_integration_test.go — optional live checks that job stdout
// is visible in Loki and that Prometheus answers queries after a batch job runs.
//
// This does not run in CI by default. It is gated on:
//   - ABC_INTEGRATION_OBS_STACK=1
//   - a readable abc CLI config (default ~/.abc/config.yaml or ABC_CONFIG_FILE)
//   - active_context with cluster_type abc-nodes, admin.services.nomad.*, enhanced
//     capabilities, and synced Loki / Prometheus admin.services URLs
//   - Nomad reachable at the configured nomad_addr (same as `abc job run --submit`)
//
// Job submission matches `abc job run --submit` without flags: this test calls
// syncNomadEnvFromABCContext so NOMAD_ADDR / NOMAD_TOKEN / NOMAD_REGION always
// come from the active context, overriding any stale ABC_* / NOMAD_* in the shell
// (ensureNomadEnv alone skips when the shell already exports both addr and token).
//
// Example:
//
//	export ABC_INTEGRATION_OBS_STACK=1
//	export ABC_CONFIG_FILE=$HOME/.abc/config.yaml
//	go test -tags integration -v -timeout=15m -run TestIntegration_ObsStack ./cmd/job/...
//
// Optional tuning:
//   ABC_INTEGRATION_LOKI_WAIT_SEC — seconds to poll Loki for the stdout sentinel (default 360)
//   ABC_INTEGRATION_LOKI_REQUIRE=0 — skip the Loki check; Prometheus check still runs
//
// Then open Grafana → Explore → Loki with LogQL:
//   {alloc_id="<uuid>"} |= "ABCOBSSMOKE"
// and Prometheus with:
//   nomad_client_uptime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// obsStackHTTPClient bounds per-request latency so a bad Loki/Prometheus URL does
// not stall the test on the default client’s very long TCP dial timeout.
var obsStackHTTPClient = &http.Client{Timeout: 25 * time.Second}

// nomadJobFailureDiagnostics fetches allocation task state for a dead/failed batch job.
func nomadJobFailureDiagnostics(t *testing.T, addr, jobID string) string {
	t.Helper()
	tok := strings.TrimSpace(os.Getenv("NOMAD_TOKEN"))
	ns := strings.TrimSpace(os.Getenv("ABC_TEST_NS"))
	if ns == "" {
		ns = "default"
	}
	listURL := fmt.Sprintf("%s/v1/job/%s/allocations?namespace=%s", strings.TrimRight(addr, "/"),
		url.PathEscape(jobID), url.QueryEscape(ns))
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return fmt.Sprintf("(alloc list request: %v)", err)
	}
	if tok != "" {
		req.Header.Set("X-Nomad-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("(alloc list: %v)", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("GET %s → %d: %s", listURL, resp.StatusCode, truncate(string(body), 500))
	}
	var stubs []struct {
		ID           string `json:"ID"`
		ClientStatus string `json:"ClientStatus"`
	}
	if err := json.Unmarshal(body, &stubs); err != nil {
		return string(body)
	}
	var b strings.Builder
	for _, st := range stubs {
		fmt.Fprintf(&b, "alloc %s client_status=%s; ", st.ID, st.ClientStatus)
		if st.ID == "" {
			continue
		}
		allocURL := fmt.Sprintf("%s/v1/allocation/%s?namespace=%s", strings.TrimRight(addr, "/"),
			url.PathEscape(st.ID), url.QueryEscape(ns))
		r2, _ := http.NewRequest(http.MethodGet, allocURL, nil)
		if tok != "" {
			r2.Header.Set("X-Nomad-Token", tok)
		}
		ar, err := http.DefaultClient.Do(r2)
		if err != nil {
			fmt.Fprintf(&b, "(get alloc: %v) ", err)
			continue
		}
		ab, _ := io.ReadAll(ar.Body)
		ar.Body.Close()
		if ar.StatusCode != http.StatusOK {
			fmt.Fprintf(&b, "GET alloc %d: %s ", ar.StatusCode, truncate(string(ab), 200))
			continue
		}
		var full struct {
			TaskStates map[string]json.RawMessage `json:"TaskStates"`
		}
		_ = json.Unmarshal(ab, &full)
		for task, raw := range full.TaskStates {
			fmt.Fprintf(&b, "task %s: %s ", task, truncate(string(raw), 400))
		}
	}
	return strings.TrimSpace(b.String())
}

// requireABCCLIContextForObsStack loads ~/.abc (or ABC_CONFIG_FILE) and checks
// the active context is suitable for this test.
func requireABCCLIContextForObsStack(t *testing.T) (cfg *config.Config, ctx config.Context) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("abc config load failed: %v (set ABC_CONFIG_FILE or create ~/.abc/config.yaml)", err)
	}
	if strings.TrimSpace(cfg.ActiveContext) == "" {
		t.Fatal("config has no active_context; run: abc context use <name>")
	}
	ctx = cfg.ActiveCtx()
	if !ctx.IsABCNodesCluster() {
		t.Fatalf("active context %q must have cluster_type abc-nodes", cfg.ActiveContext)
	}
	if strings.TrimSpace(ctx.NomadAddr()) == "" {
		t.Fatal("active context missing admin.services.nomad.nomad_addr; run: abc admin services config sync")
	}
	if strings.TrimSpace(ctx.NomadToken()) == "" {
		t.Fatal("active context missing admin.services.nomad.nomad_token; run: abc admin services config sync")
	}
	return cfg, ctx
}

// syncNomadEnvFromABCContext exports the active context's Nomad fields into the
// process environment for the duration of the test. This avoids ensureNomadEnv's
// early return when the shell already has NOMAD_ADDR+NOMAD_TOKEN (e.g. cloud
// gateway) which would otherwise hide the node ACL token from ~/.abc.
func syncNomadEnvFromABCContext(t *testing.T, ctx config.Context) {
	t.Helper()
	t.Setenv("ABC_ADDR", "")
	t.Setenv("ABC_TOKEN", "")
	t.Setenv("ABC_REGION", "")
	t.Setenv("NOMAD_ADDR", strings.TrimSpace(ctx.NomadAddr()))
	t.Setenv("NOMAD_TOKEN", strings.TrimSpace(ctx.NomadToken()))
	if r := strings.TrimSpace(ctx.NomadRegion()); r != "" {
		t.Setenv("NOMAD_REGION", r)
	}
}

func obsStackHTTPEndpoints(t *testing.T, ctx config.Context) (lokiHTTP, promHTTP string) {
	t.Helper()
	env := config.AbcNodesMonitoringEnv(ctx)
	if env == nil {
		t.Fatal("active context has no AbcNodesMonitoringEnv; run: abc cluster capabilities sync && abc admin services config sync")
	}
	lokiHTTP = strings.TrimSpace(env["ABC_NODES_LOKI_HTTP"])
	promHTTP = strings.TrimSpace(env["ABC_NODES_PROMETHEUS_HTTP"])
	if lokiHTTP == "" || promHTTP == "" {
		t.Fatalf("missing Loki or Prometheus base URL in env map: %#v", env)
	}
	return lokiHTTP, promHTTP
}

func requireObsStackIntegration(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("ABC_INTEGRATION_OBS_STACK")) != "1" {
		t.Skip(`set ABC_INTEGRATION_OBS_STACK=1 to run observability stack checks (see monitoring_stack_integration_test.go)`)
	}
}

// lokiHTTPEndpoint joins a Loki HTTP API path (e.g. "api/v1/query_range") to the
// admin-synced base URL, whether the base is "http://host:3100" or already ends
// with "/loki" (Traefik path prefix).
func lokiHTTPEndpoint(lokiBase, apiPath string) string {
	b := strings.TrimRight(strings.TrimSpace(lokiBase), "/")
	p := strings.Trim(apiPath, "/")
	if strings.HasSuffix(b, "/loki") {
		return b + "/" + p
	}
	return b + "/loki/" + p
}

func obsStackLokiPollDeadline() time.Time {
	sec := 360
	if s := strings.TrimSpace(os.Getenv("ABC_INTEGRATION_LOKI_WAIT_SEC")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			sec = n
		}
	}
	return time.Now().Add(time.Duration(sec) * time.Second)
}

func isLokiTransportErr(err error) bool {
	if err == nil {
		return false
	}
	var op *net.OpError
	return errors.As(err, &op)
}

func lokiQueryRangeContains(ctx context.Context, lokiBase, logQL, needle string) (bool, error) {
	end := time.Now()
	start := end.Add(-60 * time.Minute)
	q := url.Values{}
	q.Set("query", logQL)
	q.Set("limit", "2000")
	q.Set("start", fmt.Sprintf("%d", start.UnixNano()))
	q.Set("end", fmt.Sprintf("%d", end.UnixNano()))
	u := lokiHTTPEndpoint(lokiBase, "api/v1/query_range") + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := obsStackHTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("loki query_range: status %d body %s", resp.StatusCode, truncate(string(body), 400))
	}
	// Log lines are in data.result[].values[][1], not necessarily as a raw substring of the JSON.
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Values [][]string `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return strings.Contains(string(body), needle), nil
	}
	if envelope.Status != "success" {
		return false, fmt.Errorf("loki status %q", envelope.Status)
	}
	for _, stream := range envelope.Data.Result {
		for _, pair := range stream.Values {
			if len(pair) >= 2 && strings.Contains(pair[1], needle) {
				return true, nil
			}
		}
	}
	return false, nil
}

// lokiLabelsDebugLine returns a short hint of label names present in Loki for the
// last hour (best-effort; empty on error).
func lokiLabelsDebugLine(ctx context.Context, lokiBase string) string {
	end := time.Now()
	start := end.Add(-60 * time.Minute)
	q := url.Values{}
	q.Set("start", fmt.Sprintf("%d", start.UnixNano()))
	q.Set("end", fmt.Sprintf("%d", end.UnixNano()))
	u := lokiHTTPEndpoint(lokiBase, "api/v1/labels") + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	resp, err := obsStackHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	var envelope struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Status != "success" {
		return truncate(string(body), 200)
	}
	if len(envelope.Data) == 0 {
		return "(no labels)"
	}
	n := len(envelope.Data)
	if n > 25 {
		n = 25
	}
	return "sample labels: " + strings.Join(envelope.Data[:n], ", ")
}

func promInstantQuery(ctx context.Context, promBase, promQL string) (string, error) {
	q := url.Values{}
	q.Set("query", promQL)
	u := strings.TrimRight(promBase, "/") + "/api/v1/query?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := obsStackHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("prometheus query: status %d body %s", resp.StatusCode, truncate(string(body), 400))
	}
	var parsed struct {
		Status string `json:"status"`
		Data   struct {
			Result []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.Status != "success" {
		return string(body), fmt.Errorf("prometheus status not success: %s", parsed.Status)
	}
	if len(parsed.Data.Result) == 0 {
		return string(body), fmt.Errorf("no series for %q", promQL)
	}
	return string(body), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestIntegration_ObsStackJobStdoutReachableInLokiAndPrometheusAlive submits a
// short exec job whose stdout contains a unique sentinel, then polls Loki until
// that line appears (via Alloy → Loki path on the node) and checks Prometheus
// responds to a Nomad client metric query (so Grafana data sources have signal).
func TestIntegration_ObsStackJobStdoutReachableInLokiAndPrometheusAlive(t *testing.T) {
	requireObsStackIntegration(t)
	_, abcCtx := requireABCCLIContextForObsStack(t)
	syncNomadEnvFromABCContext(t, abcCtx)
	// Align poll/delete helpers with the namespace used by the job (abc config).
	t.Setenv("ABC_TEST_NS", abcCtx.AbcNodesNomadNamespaceOrDefault())

	addr := requireNomad(t)
	lokiBase, promBase := obsStackHTTPEndpoints(t, abcCtx)

	// Alphanumeric sentinel only so the generated shell script stays POSIX-safe.
	sentinel := fmt.Sprintf("ABCOBSSMOKE%x", time.Now().UnixNano())
	// /bin/sh + echo: avoids bash/printf edge cases on minimal exec images.
	script := fmt.Sprintf(`#!/bin/sh
#ABC --name=obs-smoke-%d
#ABC --cores=1
#ABC --mem=128M
echo %s
exit 0
`, time.Now().UnixNano(), sentinel)
	p := writeTempScript(t, "obs_stack_smoke.sh", script)
	// Submit like the operator: no --nomad-addr / --nomad-token (from ~/.abc via NomadDefaultsFromConfig).
	out, err := executeCmd(t, p, "--submit",
		"--namespace", abcCtx.AbcNodesNomadNamespaceOrDefault(),
	)
	if err != nil {
		t.Fatalf("--submit failed: %v\n%s", err, out)
	}
	jobID := extractJobID(t, out)
	t.Cleanup(func() { stopJob(t, addr, jobID) })
	status := waitForJobTerminal(t, addr, jobID, integrationTimeout())
	if status != "complete" {
		diag := nomadJobFailureDiagnostics(t, addr, jobID)
		t.Fatalf("expected job complete, got %q (nomad_addr=%s namespace=%s). Allocation/task detail:\n%s",
			status, strings.TrimSpace(os.Getenv("NOMAD_ADDR")), strings.TrimSpace(os.Getenv("ABC_TEST_NS")), diag)
	}

	// Prometheus: Nomad client metrics scraped by Alloy should exist.
	reqCtx := context.Background()
	promDeadline := time.Now().Add(2 * time.Minute)
	var promBody string
	for time.Now().Before(promDeadline) {
		body, err := promInstantQuery(reqCtx, promBase, `nomad_client_uptime`)
		if err == nil {
			promBody = body
			break
		}
		t.Logf("prometheus (retry): %v", err)
		time.Sleep(3 * time.Second)
	}
	if promBody == "" {
		t.Fatal("prometheus did not return nomad_client_uptime within timeout — check Alloy scrape + Grafana Prometheus datasource")
	}
	t.Logf("prometheus nomad_client_uptime: ok (sample len=%d)", len(promBody))

	if strings.TrimSpace(os.Getenv("ABC_INTEGRATION_LOKI_REQUIRE")) == "0" {
		t.Logf("skipping Loki sentinel check (ABC_INTEGRATION_LOKI_REQUIRE=0)")
		return
	}

	allocID := nomadJobFirstCompleteAllocID(t, addr, jobID)
	if allocID == "" {
		t.Logf("loki: no complete allocation id from Nomad; only generic LogQL selectors will be used")
	}

	// Loki: prefer alloc-scoped selectors (Alloy regex labels from Nomad log paths),
	// then broader matchers.
	var logQLs []string
	if allocID != "" {
		esc := regexp.QuoteMeta(allocID)
		logQLs = append(logQLs,
			fmt.Sprintf(`{alloc_id=%q} |= "%s"`, allocID, sentinel),
			fmt.Sprintf(`{alloc_id=%q,stream="stdout"} |= "%s"`, allocID, sentinel),
			fmt.Sprintf(`{filename=~".*/alloc/%s/alloc/logs/.*"} |= "%s"`, esc, sentinel),
		)
	}
	logQLs = append(logQLs,
		fmt.Sprintf(`{stream="stdout"} |= "%s"`, sentinel),
		fmt.Sprintf(`{stream=~".+"} |= "%s"`, sentinel),
		fmt.Sprintf(`{task="main"} |= "%s"`, sentinel),
		fmt.Sprintf(`{task=~".+"} |= "%s"`, sentinel),
		fmt.Sprintf(`{alloc_id=~".+"} |= "%s"`, sentinel),
	)

	lokiDeadline := obsStackLokiPollDeadline()
	var found bool
	var sawLokiOK bool
	var lastTransport error
	for time.Now().Before(lokiDeadline) {
		for _, logQL := range logQLs {
			ok, err := lokiQueryRangeContains(reqCtx, lokiBase, logQL, sentinel)
			if err != nil {
				t.Logf("loki query %q: %v", logQL, err)
				if isLokiTransportErr(err) {
					lastTransport = err
				} else {
					// HTTP errors (4xx/5xx body), parse errors, etc. — Loki was reached.
					sawLokiOK = true
				}
				continue
			}
			sawLokiOK = true
			if ok {
				found = true
				t.Logf("loki: matched with query %q", logQL)
				break
			}
		}
		if found {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if !found {
		hint := lokiLabelsDebugLine(reqCtx, lokiBase)
		if hint != "" {
			t.Logf("loki debug: %s", hint)
		}
		if !sawLokiOK && lastTransport != nil {
			t.Fatalf("Loki HTTP endpoint %q not reachable from this host (last error: %v). Point admin.services.loki.http at a URL you can reach (for example the same host Traefik uses), or set ABC_INTEGRATION_LOKI_REQUIRE=0 to skip the Loki assertion while still checking Prometheus.",
				strings.TrimSpace(lokiBase), lastTransport)
		}
		t.Fatalf("sentinel %q not found in Loki within timeout (alloc_id=%q) — ensure Alloy raw_exec runs on each Nomad client, nomad_alloc_log_path matches that host's Nomad alloc dir, and ABC_NODES_LOKI_HTTP matches the datasource used in Grafana. Optional: ABC_INTEGRATION_LOKI_WAIT_SEC, ABC_INTEGRATION_LOKI_REQUIRE=0 to skip this assertion.",
			sentinel, allocID)
	}
	t.Logf("loki: found sentinel in query_range response")
}
