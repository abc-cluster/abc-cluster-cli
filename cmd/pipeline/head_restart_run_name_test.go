package pipeline

import (
	"strings"
	"testing"
)

// The head passes a fixed `-name`, and Nomad restarts it into the same
// allocation, so /local (and the history file the first attempt wrote)
// survives. Nextflow's CmdRun.checkRunName then aborts the restart with
//
//	Run name `<tag>` has been already used -- Specify a different one
//
// and keeps aborting, so the head can never come back. The check is skipped
// when HistoryFile.disabled(), i.e. NXF_IGNORE_RESUME_HISTORY=true.
//
// This was previously set only inside the S3-cloudcache branch, leaving
// shared-POSIX and operator-supplied work dirs exposed. Any work dir that
// gets a fixed name must also get the guard.
func TestHCLAdapter_FixedRunNameAlwaysCarriesHistoryGuard(t *testing.T) {
	cases := []struct {
		name    string
		workDir string
	}{
		{"shared POSIX host volume", "/opt/abc-seedling/nf-work/run-1"},
		{"operator-supplied custom path", "/mnt/scratch/nf/run-1"},
		{"canonical s3 work dir", "s3://su-mbhg-hostgen/user/solar-civet/workdir/solar-civet-1780683367/"},
		{"non-canonical s3 work dir", "s3://some-bucket/arbitrary/prefix/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &PipelineSpec{
				Repository:  "https://github.com/nf-core/demo",
				WorkDir:     tc.workDir,
				Datacenters: []string{"seedling-prod"},
			}
			spec.defaults()
			// run.go sets RunTag per submission; defaults() does not. Without it
			// the generator emits no -name and the precondition below is vacuous.
			spec.RunTag = "demo-1780683367"

			hcl := generateHeadJobHCL(spec, "http://127.0.0.1:4646", "token", "run-uuid")

			// Precondition: the generator emits a fixed run name.
			if !strings.Contains(hcl, "-name "+spec.RunTag) {
				t.Fatalf("expected a fixed -name in the head command\n%s", hcl)
			}
			if !strings.Contains(hcl, "NXF_IGNORE_RESUME_HISTORY") {
				t.Errorf("fixed -name emitted without NXF_IGNORE_RESUME_HISTORY; "+
					"a restarted head will abort on the run-name collision\n--- env ---\n%s",
					extractEnvBlock(hcl))
			}
		})
	}
}
