package job

// Tests for FIX 1: the Nomad register request must target the job's own
// namespace (spec.Namespace), not the client's resolver-derived default.
//
// Nomad ACL checks the ?namespace= query param on /v1/jobs, not the
// `namespace = "..."` embedded in the job body. A pool-slot member token only
// has write access in its own namespace, so if the register targets "default"
// (or an empty/divergent namespace) while the job declares "su-mbhg-hostgen",
// Nomad returns 403 Permission denied — the live-test blocker this fixes.
//
// These tests drive runWithNomad against an httptest server (no live cluster)
// and assert the namespace seen on /v1/jobs equals spec.Namespace.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
)

// fakeNomadServer returns an httptest server emulating just enough of the Nomad
// API for runWithNomad's submit path: HCL parse, the two (skippable) preflights,
// and the register. It records the ?namespace= query observed on each endpoint.
type fakeNomadServer struct {
	srv           *httptest.Server
	parseNS       string
	registerNS    string
	registerHits  int
	canonicalJSON string
}

func newFakeNomadServer(t *testing.T, canonicalJobJSON string) *fakeNomadServer {
	t.Helper()
	f := &fakeNomadServer{canonicalJSON: canonicalJobJSON}
	mux := http.NewServeMux()

	// /v1/jobs/parse → return the canonicalized job JSON.
	mux.HandleFunc("/v1/jobs/parse", func(w http.ResponseWriter, r *http.Request) {
		f.parseNS = r.URL.Query().Get("namespace")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.canonicalJSON))
	})

	// Preflight endpoints: respond 403 so both preflights skip gracefully
	// (mirrors a real pool-slot token with no token:self / node:read access).
	mux.HandleFunc("/v1/acl/token/self", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Permission denied", http.StatusForbidden)
	})
	mux.HandleFunc("/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Permission denied", http.StatusForbidden)
	})

	// /v1/jobs (register) → record the namespace and return a fake eval.
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		f.registerHits++
		f.registerNS = r.URL.Query().Get("namespace")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"EvalID":"eval-test-123"}`))
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// registerTestCmd builds a cobra command carrying the flags nomadClientFromCmd
// reads, pointed at the fake server. The --namespace flag is left empty on
// purpose: the divergence FIX 1 addresses is precisely that the client's
// resolved namespace is NOT spec.Namespace, so the register must use the spec.
func registerTestCmd(t *testing.T, addr string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "abc"}
	root.PersistentFlags().String("nomad-addr", "", "")
	root.PersistentFlags().String("nomad-token", "", "")
	root.PersistentFlags().String("region", "", "")
	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().String("nomad-addr", "", "")
	cmd.Flags().String("nomad-token", "", "")
	cmd.Flags().String("region", "", "")
	cmd.Flags().String("namespace", "", "")
	cmd.Flags().Bool("watch", false, "")
	root.AddCommand(cmd)
	_ = cmd.Flags().Set("nomad-addr", addr)
	_ = cmd.Flags().Set("nomad-token", "member-token")
	_ = cmd.Flags().Set("region", "global")
	return cmd
}

// TestRunWithNomad_RegisterUsesSpecNamespace is the core FIX 1 guard: a real
// (non-dry-run) submit must issue the register against spec.Namespace.
func TestRunWithNomad_RegisterUsesSpecNamespace(t *testing.T) {
	// Isolate from any developer config so namespaceFromCmd can't accidentally
	// resolve the same namespace and mask the bug.
	t.Setenv("ABC_CLI_CONFIG_FILE", "/nonexistent/abc-config-for-test.yaml")

	const wantNS = "su-mbhg-hostgen"
	fake := newFakeNomadServer(t, `{"ID":"hostgen-abc12345","Namespace":"`+wantNS+`","TaskGroups":[]}`)
	cmd := registerTestCmd(t, fake.srv.URL)

	spec := &jobSpec{Name: "hostgen-abc12345", Namespace: wantNS, Region: "global"}

	err := runWithNomad(context.Background(), cmd, spec, "job \"x\" {}", true /*submit*/, false /*dryRun*/)
	if err != nil {
		t.Fatalf("runWithNomad: %v", err)
	}

	if fake.registerHits != 1 {
		t.Fatalf("expected exactly 1 register call, got %d", fake.registerHits)
	}
	if fake.registerNS != wantNS {
		t.Errorf("register targeted namespace %q, want %q (spec.Namespace)", fake.registerNS, wantNS)
	}
	// The parse must also be scoped to the job's namespace (same ACL rule).
	if fake.parseNS != wantNS {
		t.Errorf("HCL parse targeted namespace %q, want %q (spec.Namespace)", fake.parseNS, wantNS)
	}
}
