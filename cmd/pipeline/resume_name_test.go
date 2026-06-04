package pipeline

import "testing"

func TestWorkDirRunTag(t *testing.T) {
	cases := map[string]string{
		"s3://su-mbhg-hostgen/user/solar-civet/workdir/solar-civet-1780555257/": "solar-civet-1780555257",
		"s3://su-mbhg-hostgen/user/solar-civet/workdir/solar-civet-1780555257":  "solar-civet-1780555257",
		"s3://su-demo/user/slate-sunbird/workdir/slate-sunbird-1779/":           "slate-sunbird-1779",
		"s3://b/x/workdir/":                       "", // no run-tag segment
		"/scratch/local/nf-work/foo/":             "", // not s3
		"":                                        "",
		"s3://bucket/no/canonical/layout/here/":   "", // no "workdir" segment
	}
	for in, want := range cases {
		if got := workDirRunTag(in); got != want {
			t.Errorf("workDirRunTag(%q) = %q; want %q", in, got, want)
		}
	}
}
