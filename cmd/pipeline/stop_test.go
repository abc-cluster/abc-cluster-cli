package pipeline

import "testing"

// The run tag is the prefix shared by a run's head job and every worker job the
// executor submits for it, which is what makes the whole set addressable at
// once. Stopping the head alone leaves the workers running.
func TestRunTagOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abhinav-1788628378", "abhinav-1788628378"},                              // bare tag
		{"abhinav-1788628378-nf-head-mtb-resistotyper", "abhinav-1788628378"},     // head
		{"abhinav-1788628378-eecd7d55-SELECT_CONCORDANT", "abhinav-1788628378"},   // worker
		{"abhinav-1788628378-d39cd821-SELECT_FULL@default", "abhinav-1788628378"}, // Nomad UI form
		{"abhin-script-job-build_bdq-66a52cc4", ""},                               // a script job, not a run
		{"abc-node-probe-system", ""},                                             // platform job
		{"", ""},
	}
	for _, c := range cases {
		if got := runTagOf(c.in); got != c.want {
			t.Errorf("runTagOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A script job must never be mistaken for a pipeline run — `pipeline stop` on
// one would be a prefix match over unrelated work.
func TestRunTagOf_DoesNotMatchScriptJobs(t *testing.T) {
	for _, id := range []string{
		"abhin-script-job-catjob-75fb6b1c",
		"abhin-script-job-t-1a29993f",
	} {
		if got := runTagOf(id); got != "" {
			t.Errorf("runTagOf(%q) = %q, want empty — script jobs are not pipeline runs", id, got)
		}
	}
}
