package job

import "testing"

func TestParseNomadURL(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		wantErr        bool
		wantJob        string
		wantNamespace  string
		wantAlloc      string
		wantTask       string
	}{
		{
			name: "the user's URL (full — alloc + task)",
			in:   "https://nomad.seedling.abc-cluster.cloud/ui/jobs/solar-civet-1780492799-9433c853-CALL_WF_GATK_HAPLOTYPE_CALLER@su-mbhg-hostgen/allocations?activeTask=9259c1ad-ddfa-aa6e-d748-413f0d56dfb2-nf-task",
			wantJob:       "solar-civet-1780492799-9433c853-CALL_WF_GATK_HAPLOTYPE_CALLER",
			wantNamespace: "su-mbhg-hostgen",
			wantAlloc:     "9259c1ad-ddfa-aa6e-d748-413f0d56dfb2",
			wantTask:      "nf-task",
		},
		{
			name:    "bare job (no namespace, no allocations subpath)",
			in:      "https://nomad.seedling.abc-cluster.cloud/ui/jobs/hello-world",
			wantJob: "hello-world",
		},
		{
			name:          "job + namespace, no subpath",
			in:            "https://nomad.seedling.abc-cluster.cloud/ui/jobs/hello-world@su-demo",
			wantJob:       "hello-world",
			wantNamespace: "su-demo",
		},
		{
			name:          "job + namespace + allocations subpath, no activeTask",
			in:            "https://nomad.seedling.abc-cluster.cloud/ui/jobs/hello@default/allocations",
			wantJob:       "hello",
			wantNamespace: "default",
		},
		{
			name:          "host varies (grove tier)",
			in:            "https://nomad.grove.example.cloud/ui/jobs/my-job@su-x/allocations?activeTask=11111111-2222-3333-4444-555555555555-foo",
			wantJob:       "my-job",
			wantNamespace: "su-x",
			wantAlloc:     "11111111-2222-3333-4444-555555555555",
			wantTask:      "foo",
		},
		{
			name:          "task name with hyphens",
			in:            "https://nomad.grove.example/ui/jobs/j@n/allocations?activeTask=11111111-2222-3333-4444-555555555555-my-multi-dash-task",
			wantJob:       "j",
			wantNamespace: "n",
			wantAlloc:     "11111111-2222-3333-4444-555555555555",
			wantTask:      "my-multi-dash-task",
		},
		{
			name:    "http (not https) still works",
			in:      "http://localhost:4646/ui/jobs/local-test",
			wantJob: "local-test",
		},
		{
			name:    "trailing slash after subpath",
			in:      "https://nomad.x.example/ui/jobs/j@n/evaluations/",
			wantJob: "j", wantNamespace: "n",
		},
		{
			name:    "definitions sub-tab also lands cleanly",
			in:      "https://nomad.x.example/ui/jobs/j@n/definition",
			wantJob: "j", wantNamespace: "n",
		},
		{
			name:    "not a jobs URL (clients page)",
			in:      "https://nomad.x.example/ui/clients",
			wantErr: true,
		},
		{
			name:    "no path",
			in:      "https://nomad.x.example/",
			wantErr: true,
		},
		{
			name:    "ui/jobs but empty job id",
			in:      "https://nomad.x.example/ui/jobs/",
			wantErr: true,
		},
		{
			name:    "structurally broken URL",
			in:      "https://%%not-a-url",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := ParseNomadURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got ref=%+v", ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ref.JobID != tc.wantJob {
				t.Errorf("JobID = %q, want %q", ref.JobID, tc.wantJob)
			}
			if ref.Namespace != tc.wantNamespace {
				t.Errorf("Namespace = %q, want %q", ref.Namespace, tc.wantNamespace)
			}
			if ref.AllocID != tc.wantAlloc {
				t.Errorf("AllocID = %q, want %q", ref.AllocID, tc.wantAlloc)
			}
			if ref.Task != tc.wantTask {
				t.Errorf("Task = %q, want %q", ref.Task, tc.wantTask)
			}
		})
	}
}

func TestLooksLikeNomadURL(t *testing.T) {
	cases := map[string]bool{
		"https://nomad.x/ui/jobs/y":   true,
		"http://localhost:4646/":      true,
		"hello-world":                 false,
		"my-job@su-demo":              false,
		"":                            false,
		"file:///tmp/foo":             false,
	}
	for in, want := range cases {
		if got := looksLikeNomadURL(in); got != want {
			t.Errorf("looksLikeNomadURL(%q) = %v, want %v", in, got, want)
		}
	}
}
