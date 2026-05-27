package accounting

import (
	"strings"
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/capability"
)

// TestRejectAtSeedling: against an active context with no capability
// services declared (the seedling shape — nil caps), the budget verbs'
// declaration must reject with the standard capability.Require()
// failure message naming abc-controller-svc.
//
// Spec cli-verb-tree-restructure §E. Mirrors the rejection-test pattern
// in cmd/report/report_test.go (TestRejectAllContexts).
func TestRejectAtSeedling(t *testing.T) {
	d := capability.Require(accountingCapabilities, nil, nil)
	if !d.Failed() {
		t.Fatal("expected budget capabilities to fail at seedling (no services available)")
	}
	err := d.AsError()
	if err == nil {
		t.Fatal("AsError() returned nil")
	}
	if !strings.Contains(err.Error(), "abc-controller-svc") {
		t.Errorf("error doesn't reference abc-controller-svc: %v", err)
	}
}
