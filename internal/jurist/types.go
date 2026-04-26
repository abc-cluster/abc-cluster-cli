// Package jurist provides types and a client for the abc-jurist job rewriting service.
//
// abc-jurist is a cluster-side HTTP service that rewrites Nomad job HCL:
//   - resolves virtual driver values (auto-container, auto-exec) to concrete drivers
//   - injects placement constraints so allocations land on nodes with the resolved driver
//   - returns a transformation log so operators can see what changed
//
// When jurist is not reachable (admin.services.jurist.http not set or unreachable),
// ResolveLocally() provides the same resolution using capabilities.nodes from config.
package jurist

import "github.com/abc-cluster/abc-cluster-cli/internal/config"

// DriverHint constants for the two virtual driver values.
const (
	DriverAutoContainer = "auto-container"
	DriverAutoExec      = "auto-exec"
)

// IsAutoDriver reports whether d is one of the virtual auto-* driver values.
func IsAutoDriver(d string) bool {
	return d == DriverAutoContainer || d == DriverAutoExec
}

// RewriteRequest is the payload sent to POST /v1/rewrite.
type RewriteRequest struct {
	JobHCL         string                `json:"job_hcl"`
	Context        string                `json:"context,omitempty"`
	DriverPriority config.DriverPriority `json:"driver_priority"`
}

// RewriteResponse is the payload returned by POST /v1/rewrite.
type RewriteResponse struct {
	JobHCL          string               `json:"job_hcl"`
	Transformations []TransformationEntry `json:"transformations,omitempty"`
}

// TransformationEntry records a single change made during rewriting.
type TransformationEntry struct {
	Task   string `json:"task"`
	Field  string `json:"field"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// Resolution holds the result of resolving a single auto-* driver to a concrete driver.
type Resolution struct {
	// OriginalDriver is the auto-* value that was requested.
	OriginalDriver string
	// ResolvedDriver is the concrete Nomad driver name.
	ResolvedDriver string
	// EligibleNodeIDs is the list of node UUIDs that have the resolved driver healthy+detected.
	// Used to build a placement constraint instead of ${driver.NAME} which breaks for hyphenated names.
	EligibleNodeIDs []string
	// Reason explains why this driver was chosen.
	Reason string
	// Warning is set when a fallback or degraded choice was made (e.g. raw_exec chosen because no sandbox available).
	Warning string
}
