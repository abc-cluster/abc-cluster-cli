package pipeline

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// TestResolveSubmissionSource exercises the four classifier paths via a
// stand-in cobra.Command with the same flags `abc pipeline run` exposes
// (or will expose) for template and rerun. The pipeline verb does not yet
// register --template / --rerun, so the helper relies on the underlying
// classifier being defensive against unknown flags. This test guards
// against future regressions if those flags get added.
//
// Spec abc-report.md §B acceptance: a test asserting submission_source
// resolves correctly for each of the four buckets — template:<id>,
// rerun, automation, handwritten.
func TestResolveSubmissionSource(t *testing.T) {
	cases := []struct {
		name       string
		template   string
		rerun      bool
		automation string
		want       string
	}{
		{"handwritten", "", false, "", "handwritten"},
		{"automation", "", false, "1", "automation"},
		{"template", "nf-core/rnaseq", false, "", "template:nf-core/rnaseq"},
		{"rerun", "nf-core/rnaseq", true, "1", "rerun"}, // rerun wins
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ABC_AUTOMATION", tc.automation)
			cmd := &cobra.Command{}
			cmd.Flags().String("template", "", "")
			cmd.Flags().Bool("rerun", false, "")
			if tc.template != "" {
				_ = cmd.Flags().Set("template", tc.template)
			}
			if tc.rerun {
				_ = cmd.Flags().Set("rerun", "true")
			}
			got := resolveSubmissionSource(cmd)
			if got != tc.want {
				t.Errorf("resolveSubmissionSource() = %q, want %q", got, tc.want)
			}
			// Sanity-check the underlying classifier directly too —
			// guards against a future caller plumbing bad inputs.
			direct := state.SubmissionSourceClassifier{
				TemplateID: tc.template,
				Rerun:      tc.rerun,
			}.Resolve()
			if direct != tc.want {
				t.Errorf("classifier direct = %q, want %q", direct, tc.want)
			}
		})
	}
}
