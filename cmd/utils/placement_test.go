package utils

import (
	"strings"
	"testing"
)

// B9: a blocked eval must yield an actionable placement-failure reason instead
// of an infinite silent wait.
func TestPlacementFailureReason(t *testing.T) {
	if r := PlacementFailureReason(nil); r != "" {
		t.Errorf("empty evals → %q, want empty", r)
	}
	// A "complete" eval with no failed allocs is not a failure.
	if r := PlacementFailureReason([]NomadEvaluation{{Status: "complete"}}); r != "" {
		t.Errorf("no FailedTGAllocs → %q, want empty", r)
	}
	evals := []NomadEvaluation{{
		Status: "complete",
		FailedTGAllocs: map[string]NomadAllocMetric{
			"batch": {
				NodesEvaluated:     3,
				NodesFiltered:      3,
				ConstraintFiltered: map[string]int{"missing-node-pool": 3},
			},
		},
	}}
	r := PlacementFailureReason(evals)
	if !strings.Contains(r, "batch") || !strings.Contains(r, "missing-node-pool") {
		t.Errorf("reason = %q, want mention of task group + constraint", r)
	}
}
