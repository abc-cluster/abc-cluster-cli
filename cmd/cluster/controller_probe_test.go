package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// TestProbeController_HappyPath: abc-controller-svc returns a well-formed v1 capability
// payload; probeController parses and returns it; applyControllerResponse copies
// every field into the Capabilities struct including ProbeSource,
// Services map, Warnings.
func TestProbeController_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request shape per the brainstorm.
		if r.URL.Path != controllerCapabilitiesPath {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/abc.capability.v1+json" {
			t.Errorf("missing schema-version Accept header; got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("missing Bearer token; got %q", got)
		}
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "abc-cluster-cli/") {
			t.Errorf("expected abc-cluster-cli User-Agent; got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controllerCapabilityResponse{
			SchemaVersion: 1,
			ProbeSource:   "controller-aggregate",
			Services: map[string]controllerCapabilityResponseService{
				"abc-bitemporal-svc": {
					Codename:  "Chiranjivi",
					Available: true,
					Version:   "0.4.2",
					Features:  []string{"bitemporal-query", "lucene-search"},
					Endpoints: map[string]string{"pgwire": "100.70.185.46:15432"},
				},
				"abc-fleet-svc": {
					Available: false,
					Reason:    "Veld v1 not yet shipped",
					Fallback:  "tier-default rate card from defaults_za.go",
				},
			},
			Warnings: []string{"abc-grid-intensity-svc poller hit a 429; rate-limited until 14:30 UTC"},
		})
	}))
	defer srv.Close()

	resp, err := probeController(context.Background(), srv.URL, "secret-token", "v0.1.25+abc123")
	if err != nil {
		t.Fatalf("probeController: %v", err)
	}
	if resp.SchemaVersion != 1 {
		t.Errorf("expected schema_version=1; got %d", resp.SchemaVersion)
	}
	if resp.ProbeSource != "controller-aggregate" {
		t.Errorf("expected probe_source=controller-aggregate; got %q", resp.ProbeSource)
	}
	if len(resp.Services) != 2 {
		t.Fatalf("expected 2 services; got %d", len(resp.Services))
	}

	// Round-trip into the Capabilities struct.
	caps := &cfg.Capabilities{}
	applyControllerResponse(caps, resp)

	if caps.ProbeSource != "controller-aggregate" {
		t.Errorf("ProbeSource not propagated; got %q", caps.ProbeSource)
	}
	chiranjivi := caps.Services["abc-bitemporal-svc"]
	if !chiranjivi.Available || chiranjivi.Version != "0.4.2" {
		t.Errorf("bitemporal-svc fields lost; got %+v", chiranjivi)
	}
	if !contains(chiranjivi.Features, "lucene-search") {
		t.Errorf("expected lucene-search feature; got %v", chiranjivi.Features)
	}
	if chiranjivi.Endpoints["pgwire"] != "100.70.185.46:15432" {
		t.Errorf("endpoint not propagated; got %v", chiranjivi.Endpoints)
	}
	veld := caps.Services["abc-fleet-svc"]
	if veld.Available {
		t.Errorf("expected abc-fleet-svc unavailable")
	}
	if veld.Reason == "" || veld.Fallback == "" {
		t.Errorf("expected reason+fallback for abc-fleet-svc; got %+v", veld)
	}
	if len(caps.ProbeWarnings) != 1 {
		t.Errorf("expected 1 warning; got %d", len(caps.ProbeWarnings))
	}
	if time.Since(caps.LastSynced) > 5*time.Second {
		t.Errorf("LastSynced not refreshed; got %v", caps.LastSynced)
	}
}

// TestProbeController_SchemaVersionMismatch: server returns 406 Not
// Acceptable; probeController surfaces an "upgrade abc CLI" error per the
// brainstorm's negotiation strategy.
func TestProbeController_SchemaVersionMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "supported: v2", http.StatusNotAcceptable)
	}))
	defer srv.Close()

	_, err := probeController(context.Background(), srv.URL, "tok", "v0.1.25")
	if err == nil {
		t.Fatal("expected error for 406; got nil")
	}
	if !strings.Contains(err.Error(), "upgrade abc CLI") {
		t.Errorf("error should mention 'upgrade abc CLI'; got: %v", err)
	}
}

// TestProbeController_ServerError: any non-2xx surfaces an error with the
// status code visible.
func TestProbeController_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := probeController(context.Background(), srv.URL, "tok", "v0.1.25")
	if err == nil {
		t.Fatal("expected error for 500; got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should include status 500; got: %v", err)
	}
}

// TestProbeController_UnreachableNoFallback: a closed-connection server
// produces an error. The caller (runCapabilitiesSyncController) is responsible
// for NOT silently falling through to Nomad — this test pins that
// probeController itself simply returns an error.
func TestProbeController_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// will be closed below
	}))
	srv.Close() // close immediately so the URL refuses connections

	_, err := probeController(context.Background(), srv.URL, "tok", "v0.1.25")
	if err == nil {
		t.Fatal("expected error against closed server; got nil")
	}
}

// TestApplyControllerResponse_PreservesSeedlingBooleans: when an abc-controller-svc response
// arrives, it must NOT clobber the abc-nodes-tier shorthand booleans
// (storage / uploads / logging / etc.) — those are populated by Nomad
// introspection only, and the two probe paths can coexist for a cluster
// that runs both abc-nodes infrastructure AND a future abc-controller-svc endpoint.
func TestApplyControllerResponse_PreservesSeedlingBooleans(t *testing.T) {
	caps := &cfg.Capabilities{
		Storage: "rustfs",
		Logging: true,
	}
	applyControllerResponse(caps, &controllerCapabilityResponse{
		SchemaVersion: 1,
		Services:      map[string]controllerCapabilityResponseService{},
	})
	if caps.Storage != "rustfs" {
		t.Errorf("Storage clobbered; got %q", caps.Storage)
	}
	if !caps.Logging {
		t.Errorf("Logging clobbered")
	}
}

// contains is the same helper internal/capability uses; duplicated for
// the test file's locality.
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
