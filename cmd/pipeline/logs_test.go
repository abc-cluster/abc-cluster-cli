package pipeline

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
)

func TestParseChildJob(t *testing.T) {
	run := "solar-civet-1780492799"
	cases := []struct {
		name    string
		jobID   string
		wantOK  bool
		wantPrc string
		wantHsh string
	}{
		{
			name:    "real MAGMA child",
			jobID:   "solar-civet-1780492799-9433c853-CALL_WF_GATK_HAPLOTYPE_CALLER",
			wantOK:  true, wantPrc: "CALL_WF_GATK_HAPLOTYPE_CALLER", wantHsh: "9433c853",
		},
		{
			name:    "samplesheet validation",
			jobID:   "solar-civet-1780492799-9c19b125-VALIDATE_FASTQS_WF_SAMPLESHEET_VALIDATION",
			wantOK:  true, wantPrc: "VALIDATE_FASTQS_WF_SAMPLESHEET_VALIDATION", wantHsh: "9c19b125",
		},
		{
			name:   "head job is not a child",
			jobID:  "solar-civet-1780492799-nf-head-torch-consortium-magma",
			wantOK: false,
		},
		{
			name:   "different run prefix → no match",
			jobID:  "other-run-1234-9433c853-FOO",
			wantOK: false,
		},
		{
			name:   "hash segment not 8 hex → reject",
			jobID:  "solar-civet-1780492799-NOTAHASH-FOO",
			wantOK: false,
		},
		{
			name:   "missing process segment → reject",
			jobID:  "solar-civet-1780492799-9433c853",
			wantOK: false,
		},
		{
			name:    "process name with many underscores preserved",
			jobID:   "solar-civet-1780492799-abcdef01-MERGE_WF_SNP_ANALYSIS_OPTIMIZE_VARIANT_RECALIBRATION_GATK_VARIANT_RECALIBRATOR__ANN2",
			wantOK:  true, wantHsh: "abcdef01",
			wantPrc: "MERGE_WF_SNP_ANALYSIS_OPTIMIZE_VARIANT_RECALIBRATION_GATK_VARIANT_RECALIBRATOR__ANN2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prc, hsh, ok := parseChildJob(tc.jobID, run)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (prc=%q hsh=%q)", ok, tc.wantOK, prc, hsh)
			}
			if !ok {
				return
			}
			if prc != tc.wantPrc {
				t.Errorf("process=%q want %q", prc, tc.wantPrc)
			}
			if hsh != tc.wantHsh {
				t.Errorf("hash=%q want %q", hsh, tc.wantHsh)
			}
		})
	}
}

func TestNormalizeRunArg(t *testing.T) {
	cases := map[string]string{
		"solar-civet-1780492799":                                     "solar-civet-1780492799",
		"solar-civet-1780492799-nf-head-torch-consortium-magma":      "solar-civet-1780492799",
		"https://nomad.seedling.abc-cluster.cloud/ui/jobs/solar-civet-1780492799-nf-head-torch-consortium-magma@su-mbhg-hostgen/allocations": "solar-civet-1780492799",
		"https://nomad.grove.example/ui/jobs/abhi-123-nf-head-hello@default": "abhi-123",
	}
	for in, want := range cases {
		if got := normalizeRunArg(in); got != want {
			t.Errorf("normalizeRunArg(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestMatchFilter(t *testing.T) {
	r := taskRec{Process: "CALL_WF_LOFREQ_CALL", Status: "failed", Hash: "7b7ca2c4", JobID: "run-7b7ca2c4-CALL_WF_LOFREQ_CALL"}
	cases := []struct {
		expr string
		want bool
		err  bool
	}{
		{`status == failed`, true, false},
		{`status == completed`, false, false},
		{`status != completed`, true, false},
		{`process =~ LOFREQ`, true, false},
		{`process =~ ^MERGE`, false, false},
		{`status == failed && process =~ LOFREQ`, true, false},
		{`status == completed && process =~ LOFREQ`, false, false},
		{`status == completed || process =~ LOFREQ`, true, false},
		{`hash == 7b7ca2c4`, true, false},
		{`status == FAILED`, true, false}, // case-insensitive
		{`bogus == x`, false, true},       // unknown field
		{`status badop x`, false, true},   // no recognised operator
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := matchFilter(r, tc.expr)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error for %q", tc.expr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("matchFilter(%q) = %v; want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestDisplayPipelineStatus(t *testing.T) {
	mk := func(status string, failed, complete int) utils.NomadJobStub {
		return utils.NomadJobStub{
			Status: status,
			JobSummary: utils.NomadJobSummary{
				Summary: map[string]utils.NomadTaskGroupSummary{
					"g": {Failed: failed, Complete: complete},
				},
			},
		}
	}
	cases := []struct {
		name string
		stub utils.NomadJobStub
		want string
	}{
		{"running", mk("running", 0, 0), "running"},
		{"pending", mk("pending", 0, 0), "pending"},
		{"dead+complete → completed", mk("dead", 0, 1), "completed"},
		{"dead+failed → failed", mk("dead", 1, 0), "failed"},
		{"dead+both → failed wins", mk("dead", 1, 1), "failed"},
		{"dead+neither → dead", mk("dead", 0, 0), "dead"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayPipelineStatus(tc.stub); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveTaskDirMatch(t *testing.T) {
	// resolveTaskDir's matching logic, exercised via a fake ls.
	// hash8 "9433c853" → prefix "<wd>/94/", match subdir starting with "33c853".
	wd := "s3://b/user/u/workdir/run/"
	entries := []string{
		wd + "94/33c8533c024bf320761b167f302f1d/",
		wd + "94/9999999999999999999999999999/",
	}
	got, ok := matchHashDir(entries, wd+"94/", "33c853")
	if !ok || got != wd+"94/33c8533c024bf320761b167f302f1d/" {
		t.Errorf("matchHashDir = %q, %v", got, ok)
	}
	if _, ok := matchHashDir(entries, wd+"94/", "deadbe"); ok {
		t.Error("expected no match for absent prefix")
	}
}
