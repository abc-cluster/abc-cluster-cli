package utils

import "testing"

// TestResolveStreamTask guards the head-log task-name resolution that
// `abc pipeline run … --wait --logs` relies on. The bug it regresses: the
// old code defaulted to "main" and only overrode it via a non-deterministic
// map range, leaving "main" in place when TaskStates was still empty — which
// Nomad rejects with 400 "unknown task name \"main\"".
func TestResolveStreamTask(t *testing.T) {
	states := func(names ...string) map[string]NomadTaskState {
		m := make(map[string]NomadTaskState, len(names))
		for _, n := range names {
			m[n] = NomadTaskState{}
		}
		return m
	}

	cases := []struct {
		name       string
		override   string
		taskStates map[string]NomadTaskState
		want       string
	}{
		{
			// The failure mode: alloc created but TaskStates not yet
			// populated. Must NOT return "main".
			name:       "empty TaskStates → nf-task (never main)",
			override:   "",
			taskStates: nil,
			want:       "nf-task",
		},
		{
			name:       "head alloc reports nf-task → nf-task",
			override:   "",
			taskStates: states("nf-task"),
			want:       "nf-task",
		},
		{
			name:       "nextflow preferred alias resolves",
			override:   "",
			taskStates: states("nextflow"),
			want:       "nextflow",
		},
		{
			name:       "explicit override honoured verbatim",
			override:   "logshipper",
			taskStates: states("nf-task", "logshipper"),
			want:       "logshipper",
		},
		{
			name:       "single non-preferred task → that task",
			override:   "",
			taskStates: states("solo"),
			want:       "solo",
		},
		{
			// Deterministic: multiple non-preferred tasks pick the
			// lexicographically-first, not a random map key.
			name:       "multiple non-preferred → lexicographically first",
			override:   "",
			taskStates: states("zeta", "alpha", "mu"),
			want:       "alpha",
		},
		{
			// Preference wins even when other tasks are present.
			name:       "nf-task preferred over sibling tasks",
			override:   "",
			taskStates: states("aaa", "nf-task", "zzz"),
			want:       "nf-task",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveStreamTask(tc.override, tc.taskStates)
			if got != tc.want {
				t.Fatalf("resolveStreamTask(%q, %v) = %q; want %q", tc.override, tc.taskStates, got, tc.want)
			}
			if got == "main" {
				t.Fatalf("resolveStreamTask must never return \"main\" (regresses the 400 unknown-task bug)")
			}
		})
	}
}
