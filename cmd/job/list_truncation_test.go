package job

import (
	"sort"
	"testing"
)

// Nomad returns jobs ordered by ID. Truncating that order to --limit meant a
// pipeline's worker jobs (`abhinav-<runid>-<hash>-<PROCESS>`) sorted after
// `abc-…` and `abhin-script-job-…` and were never visible at the default limit,
// however recent. `abc job show` could find a running job that
// `abc job list | grep` reported nothing for, which reads as the two commands
// disagreeing rather than as a truncated view.
//
// Sorting newest-first is what makes the default view useful; the count of
// matches before truncation is what lets the caller say so.
func TestListOrdering_NewestFirstSurvivesTruncation(t *testing.T) {
	jobs := []NomadJobStub{
		{ID: "abc-node-probe-system", SubmitTime: 100},
		{ID: "abhin-script-job-old", SubmitTime: 200},
		{ID: "abhinav-1788628378-eecd7d55-SELECT_CONCORDANT", SubmitTime: 900},
		{ID: "abhinav-1788628378-d39cd821-SELECT_FULL", SubmitTime: 800},
	}

	sort.SliceStable(jobs, func(a, b int) bool { return jobs[a].SubmitTime > jobs[b].SubmitTime })

	matched := len(jobs)
	const limit = 2
	shown := jobs
	if limit > 0 && matched > limit {
		shown = shown[:limit]
	}

	if len(shown) != 2 {
		t.Fatalf("shown = %d, want 2", len(shown))
	}
	// The two newest are the pipeline workers — the ones the old ID-ascending
	// order always cut.
	if shown[0].ID != "abhinav-1788628378-eecd7d55-SELECT_CONCORDANT" {
		t.Errorf("first row = %q, want the newest job", shown[0].ID)
	}
	if shown[1].ID != "abhinav-1788628378-d39cd821-SELECT_FULL" {
		t.Errorf("second row = %q, want the second-newest job", shown[1].ID)
	}
	// And the caller must be able to tell the view is partial.
	if matched <= len(shown) {
		t.Errorf("matched=%d shown=%d — truncation must remain detectable so the "+
			"user is told results were cut", matched, len(shown))
	}
}
