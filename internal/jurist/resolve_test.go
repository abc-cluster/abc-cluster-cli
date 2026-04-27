package jurist_test

import (
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/jurist"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func nodes(pairs ...string) []config.NodeCapability {
	var out []config.NodeCapability
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, config.NodeCapability{
			ID:      pairs[i],
			Drivers: strings.Split(pairs[i+1], ","),
		})
	}
	return out
}

var defaultPriority = config.DriverPriority{
	ContainerPriority: []string{"containerd-driver", "docker", "podman"},
	ExecPriority:      []string{"exec2", "exec", "raw_exec"},
}

// ── auto-container ────────────────────────────────────────────────────────────

func TestResolveLocally_ContainerdFirst(t *testing.T) {
	ns := nodes("id-aaa", "containerd-driver,exec,raw_exec")
	res, err := jurist.ResolveLocally(jurist.DriverAutoContainer, ns, defaultPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ResolvedDriver != "containerd-driver" {
		t.Errorf("expected containerd-driver, got %q", res.ResolvedDriver)
	}
	if res.OriginalDriver != jurist.DriverAutoContainer {
		t.Errorf("expected original driver auto-container, got %q", res.OriginalDriver)
	}
	if len(res.EligibleNodeIDs) != 1 || res.EligibleNodeIDs[0] != "id-aaa" {
		t.Errorf("expected [id-aaa], got %v", res.EligibleNodeIDs)
	}
	if res.Warning != "" {
		t.Errorf("expected no warning for containerd-driver, got %q", res.Warning)
	}
}

func TestResolveLocally_DockerFallback(t *testing.T) {
	// 6 nodes have docker but not containerd-driver.
	ns := []config.NodeCapability{
		{ID: "id-001", Drivers: []string{"docker", "exec"}},
		{ID: "id-002", Drivers: []string{"docker", "exec"}},
		{ID: "id-003", Drivers: []string{"docker", "exec"}},
		{ID: "id-004", Drivers: []string{"docker", "exec"}},
		{ID: "id-005", Drivers: []string{"docker", "exec"}},
		{ID: "id-006", Drivers: []string{"docker", "exec"}},
	}
	res, err := jurist.ResolveLocally(jurist.DriverAutoContainer, ns, defaultPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ResolvedDriver != "docker" {
		t.Errorf("expected docker, got %q", res.ResolvedDriver)
	}
	if len(res.EligibleNodeIDs) != 6 {
		t.Errorf("expected 6 eligible nodes, got %d", len(res.EligibleNodeIDs))
	}
}

func TestResolveLocally_AutoContainerNoMatch(t *testing.T) {
	// Nodes only have exec drivers — no container driver matches priority list.
	ns := nodes("id-aaa", "exec,raw_exec")
	_, err := jurist.ResolveLocally(jurist.DriverAutoContainer, ns, defaultPriority)
	if err == nil {
		t.Fatal("expected error when no container driver available")
	}
	if !strings.Contains(err.Error(), "auto-container") {
		t.Errorf("expected auto-container in error, got: %v", err)
	}
}

// ── auto-exec ─────────────────────────────────────────────────────────────────

func TestResolveLocally_AutoExecToExec(t *testing.T) {
	ns := nodes("id-exec", "exec,raw_exec")
	res, err := jurist.ResolveLocally(jurist.DriverAutoExec, ns, defaultPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// exec2 is first in priority list but not available; exec should win.
	if res.ResolvedDriver != "exec" {
		t.Errorf("expected exec, got %q", res.ResolvedDriver)
	}
}

func TestResolveLocally_RawExecFallbackWarning(t *testing.T) {
	// Only raw_exec available — should resolve but with a warning.
	ns := nodes("id-raw", "raw_exec")
	res, err := jurist.ResolveLocally(jurist.DriverAutoExec, ns, defaultPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ResolvedDriver != "raw_exec" {
		t.Errorf("expected raw_exec, got %q", res.ResolvedDriver)
	}
	if res.Warning == "" {
		t.Error("expected warning for raw_exec fallback")
	}
	if !strings.Contains(strings.ToLower(res.Warning), "raw_exec") {
		t.Errorf("expected raw_exec in warning, got: %q", res.Warning)
	}
}

func TestResolveLocally_AutoExecNoMatch(t *testing.T) {
	// Nodes only have container drivers.
	ns := nodes("id-docker", "docker,containerd-driver")
	_, err := jurist.ResolveLocally(jurist.DriverAutoExec, ns, defaultPriority)
	if err == nil {
		t.Fatal("expected error when no exec driver available")
	}
	if !strings.Contains(err.Error(), "auto-exec") {
		t.Errorf("expected auto-exec in error, got: %v", err)
	}
}

// ── priority order ────────────────────────────────────────────────────────────

func TestResolveLocally_FirstPriorityUnavailableSecondWins(t *testing.T) {
	// containerd-driver not available, docker is — docker should win over podman
	// even though podman comes after docker in the priority list.
	ns := nodes("id-podman", "podman,exec", "id-docker", "docker,exec")
	res, err := jurist.ResolveLocally(jurist.DriverAutoContainer, ns, defaultPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ResolvedDriver != "docker" {
		t.Errorf("expected docker (higher priority than podman), got %q", res.ResolvedDriver)
	}
}

func TestResolveLocally_CustomPriorityOverridesDefault(t *testing.T) {
	ns := nodes("id-podman", "podman,exec", "id-docker", "docker,exec")
	custom := config.DriverPriority{
		ContainerPriority: []string{"podman", "docker"},
	}
	res, err := jurist.ResolveLocally(jurist.DriverAutoContainer, ns, custom)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ResolvedDriver != "podman" {
		t.Errorf("expected podman (custom priority), got %q", res.ResolvedDriver)
	}
}

// ── EligibleNodeIDs ───────────────────────────────────────────────────────────

func TestResolveLocally_EligibleNodeIDs_OnlyMatchingNodes(t *testing.T) {
	// Three nodes; two have docker, one does not.
	ns := []config.NodeCapability{
		{ID: "id-001", Drivers: []string{"docker"}},
		{ID: "id-002", Drivers: []string{"exec"}},
		{ID: "id-003", Drivers: []string{"docker"}},
	}
	res, err := jurist.ResolveLocally(jurist.DriverAutoContainer, ns, defaultPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ResolvedDriver != "docker" {
		t.Fatalf("expected docker, got %q", res.ResolvedDriver)
	}
	// Only nodes with docker should appear.
	if len(res.EligibleNodeIDs) != 2 {
		t.Errorf("expected 2 eligible node IDs, got %d: %v", len(res.EligibleNodeIDs), res.EligibleNodeIDs)
	}
	for _, id := range res.EligibleNodeIDs {
		if id == "id-002" {
			t.Errorf("id-002 (exec only) should not be in eligible list")
		}
	}
}

// ── edge cases ────────────────────────────────────────────────────────────────

func TestResolveLocally_EmptyNodes(t *testing.T) {
	_, err := jurist.ResolveLocally(jurist.DriverAutoContainer, nil, defaultPriority)
	if err == nil {
		t.Fatal("expected error for empty nodes")
	}
	if !strings.Contains(err.Error(), "capabilities sync") {
		t.Errorf("expected capabilities sync hint in error, got: %v", err)
	}
}

func TestResolveLocally_UnknownHint(t *testing.T) {
	ns := nodes("id-aaa", "docker,exec")
	_, err := jurist.ResolveLocally("auto-gpu", ns, defaultPriority)
	if err == nil {
		t.Fatal("expected error for unknown auto-* hint")
	}
	if !strings.Contains(err.Error(), "auto-gpu") {
		t.Errorf("expected hint name in error, got: %v", err)
	}
}

func TestResolveLocally_CaseInsensitiveDriverMatch(t *testing.T) {
	// Driver stored in capabilities as mixed-case (e.g. from Nomad API).
	ns := nodes("id-aaa", "Docker,Exec")
	res, err := jurist.ResolveLocally(jurist.DriverAutoContainer, ns, defaultPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ResolvedDriver != "docker" {
		t.Errorf("expected docker (case-insensitive), got %q", res.ResolvedDriver)
	}
}

func TestResolveLocally_ReasonNonEmpty(t *testing.T) {
	ns := nodes("id-aaa", "containerd-driver,exec")
	res, err := jurist.ResolveLocally(jurist.DriverAutoContainer, ns, defaultPriority)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason == "" {
		t.Error("expected non-empty Reason in Resolution")
	}
}
