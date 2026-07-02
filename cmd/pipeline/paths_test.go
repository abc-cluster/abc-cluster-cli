package pipeline

import (
	"strings"
	"testing"
)

func TestDeriveCloudCachePath(t *testing.T) {
	cases := []struct {
		name    string
		workDir string
		want    string
	}{
		{
			name:    "canonical user-scope workdir → user-scope cache",
			workDir: "s3://su-demo/user/slate-sunbird/workdir/slate-sunbird-1779823087/",
			want:    "s3://su-demo/user/slate-sunbird/cache/slate-sunbird-1779823087/",
		},
		{
			name:    "canonical group-scope workdir → group-scope cache",
			workDir: "s3://su-sdsct-ceri/group/eduan/workdir/eduan-1779808404/",
			want:    "s3://su-sdsct-ceri/group/eduan/cache/eduan-1779808404/",
		},
		{
			name:    "canonical workdir without trailing slash still parses",
			workDir: "s3://su-demo/user/slate-sunbird/workdir/slate-sunbird-1779823087",
			want:    "s3://su-demo/user/slate-sunbird/cache/slate-sunbird-1779823087/",
		},
		{
			name:    "non-s3 work dir → empty (CloudCache not enabled)",
			workDir: "/local/scratch/work/",
			want:    "",
		},
		{
			name:    "operator-supplied path not following workdir/ layout → empty",
			workDir: "s3://other-bucket/some/random/path/",
			want:    "",
		},
		{
			name:    "shallow s3 path missing run-tag → empty",
			workDir: "s3://su-demo/user/slate-sunbird/workdir/",
			want:    "",
		},
		{
			name:    "wrong segment (results instead of workdir) → empty",
			workDir: "s3://su-demo/user/slate-sunbird/results/slate-sunbird-1779823087/",
			want:    "",
		},
		{
			name:    "empty string → empty",
			workDir: "",
			want:    "",
		},
		{
			name:    "s3 prefix only → empty",
			workDir: "s3://",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveCloudCachePath(tc.workDir)
			if got != tc.want {
				t.Errorf("deriveCloudCachePath(%q) = %q, want %q", tc.workDir, got, tc.want)
			}
		})
	}
}

// TestDeriveCloudCachePath_ResumeIdempotent verifies that parsing the same
// workdir on a fresh run vs a resume run produces the same cache path —
// this is the property that lets Nextflow's CloudCache find the prior
// session's index on resume.
func TestDeriveCloudCachePath_ResumeIdempotent(t *testing.T) {
	workDir := "s3://su-demo/user/slate-sunbird/workdir/slate-sunbird-1779823087/"
	first := deriveCloudCachePath(workDir)
	second := deriveCloudCachePath(workDir)
	if first == "" {
		t.Fatalf("expected canonical workdir to derive a cache path, got empty")
	}
	if first != second {
		t.Errorf("derivation not idempotent: first=%q second=%q", first, second)
	}
}

func TestBucketFromS3URI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"s3://bucket/key", "bucket"},
		{"s3://bucket/", "bucket"},
		{"s3://bucket", "bucket"},
		{"s3://", ""},
		{"/local/path", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := bucketFromS3URI(tc.in); got != tc.want {
			t.Errorf("bucketFromS3URI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveScope(t *testing.T) {
	cases := []struct {
		name       string
		visibility string
		share      bool
		want       string
		wantErr    bool
	}{
		{"default → user", "", false, "user", false},
		{"--share → group", "", true, "group", false},
		{"--visibility=user explicit", "user", false, "user", false},
		{"--visibility=group explicit", "group", false, "group", false},
		{"--visibility=user overrides --share", "user", true, "user", false},
		{"case-insensitive visibility", "GROUP", false, "group", false},
		{"whitespace trimmed", "  user  ", false, "user", false},
		{"invalid visibility errors", "public", false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveScope(tc.visibility, tc.share)
			if (err != nil) != tc.wantErr {
				t.Fatalf("resolveScope(%q,%v) err=%v wantErr=%v", tc.visibility, tc.share, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("resolveScope(%q,%v) = %q, want %q", tc.visibility, tc.share, got, tc.want)
			}
		})
	}
}

func TestDerivedWorkDirAndOutDir(t *testing.T) {
	want_wd := "s3://su-demo/user/slate-sunbird/workdir/slate-sunbird-123/"
	if got := derivedWorkDir("su-demo", "user", "slate-sunbird", "slate-sunbird-123"); got != want_wd {
		t.Errorf("derivedWorkDir = %q want %q", got, want_wd)
	}
	want_od := "s3://su-demo/user/slate-sunbird/results/slate-sunbird-123/"
	if got := derivedOutDir("su-demo", "user", "slate-sunbird", "slate-sunbird-123"); got != want_od {
		t.Errorf("derivedOutDir = %q want %q", got, want_od)
	}
	// workdir base + cache base share scope/user/run-tag — the resume-cycle
	// invariant: parsing the workdir yields the cache, and they're siblings.
	wd := derivedWorkDir("su-demo", "user", "slate-sunbird", "slate-sunbird-123")
	cache := deriveCloudCachePath(wd)
	wantCache := "s3://su-demo/user/slate-sunbird/cache/slate-sunbird-123/"
	if cache != wantCache {
		t.Errorf("sibling invariant broken: deriveCloudCachePath(derivedWorkDir(...)) = %q want %q", cache, wantCache)
	}
}

func TestPreflightWorkDirDerivation(t *testing.T) {
	cases := []struct {
		name            string
		workDirExplicit bool
		s3Context       bool
		groupBucket     string
		wantErr         bool
	}{
		{
			// The reproduced bug: group-less user, S3 work-dir context, no
			// bucket to derive from → must fail fast, not fall through to
			// the inaccessible s3://nextflow-work/.
			name:            "auto-derive + S3 context + empty bucket → error",
			workDirExplicit: false,
			s3Context:       true,
			groupBucket:     "",
			wantErr:         true,
		},
		{
			name:            "auto-derive + S3 context + whitespace bucket → error",
			workDirExplicit: false,
			s3Context:       true,
			groupBucket:     "   ",
			wantErr:         true,
		},
		{
			// Explicit --work-dir must always be honoured, even on an S3
			// context with no group bucket.
			name:            "explicit work-dir + S3 context + empty bucket → ok",
			workDirExplicit: true,
			s3Context:       true,
			groupBucket:     "",
			wantErr:         false,
		},
		{
			// Genuine host-volume context (no S3 endpoint): /work/nextflow-work
			// is valid, so an empty bucket must NOT error.
			name:            "auto-derive + host-volume context + empty bucket → ok",
			workDirExplicit: false,
			s3Context:       false,
			groupBucket:     "",
			wantErr:         false,
		},
		{
			// Normal S3 case: bucket present → derivation works, no error.
			name:            "auto-derive + S3 context + real bucket → ok",
			workDirExplicit: false,
			s3Context:       true,
			groupBucket:     "su-mbhg-bioinformatics",
			wantErr:         false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := preflightWorkDirDerivation(tc.workDirExplicit, tc.s3Context, tc.groupBucket)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.wantErr {
				msg := err.Error()
				for _, want := range []string{"cannot auto-derive an S3 work-dir", "--work-dir", "group"} {
					if !strings.Contains(msg, want) {
						t.Errorf("error message %q missing %q", msg, want)
					}
				}
			}
		})
	}
}
