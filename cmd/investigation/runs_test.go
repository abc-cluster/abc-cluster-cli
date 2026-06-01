package investigation

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
)

// TestTagValue covers the per-run tag lookup used by `runs --by`.
func TestTagValue(t *testing.T) {
	tags := []string{"model=rf", "notebook=random-forest"}
	if got := tagValue(tags, "model"); got != "rf" {
		t.Errorf("tagValue model: got %q want rf", got)
	}
	if got := tagValue(tags, "notebook"); got != "random-forest" {
		t.Errorf("tagValue notebook: got %q want random-forest", got)
	}
	if got := tagValue(tags, "missing"); got != "" {
		t.Errorf("tagValue missing: got %q want empty", got)
	}
}

// TestGroupRunsByModel covers spec B4: --by model over runs tagged
// model=knn|rf|svm yields one group per model value.
func TestGroupRunsByModel(t *testing.T) {
	runs := []state.Run{
		{RunID: "a", Tags: []string{"model=knn", "notebook=knn"}},
		{RunID: "b", Tags: []string{"model=rf", "notebook=random-forest"}},
		{RunID: "c", Tags: []string{"model=svm", "notebook=svm"}},
		{RunID: "d", Tags: []string{"model=rf", "notebook=rf-v2"}}, // second rf run
		{RunID: "e", Tags: []string{"notebook=scratch"}},           // no model tag
	}
	groups := map[string]int{}
	for _, r := range runs {
		groups[tagValue(r.Tags, "model")]++
	}
	if groups["knn"] != 1 || groups["svm"] != 1 {
		t.Errorf("expected 1 each for knn/svm, got %v", groups)
	}
	if groups["rf"] != 2 {
		t.Errorf("expected 2 rf runs, got %d", groups["rf"])
	}
	if groups[""] != 1 {
		t.Errorf("expected 1 untagged run in the empty group, got %d", groups[""])
	}
	if len(groups) != 4 { // knn, rf, svm, "" → 4 distinct groups
		t.Errorf("expected 4 groups, got %d (%v)", len(groups), groups)
	}
}
